package countries

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"

	qwibi "github.com/qwibi/qwibi-go-sdk"
	pb "github.com/qwibi/qwibi-proto-go/qwibi/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

const testKey = "qwibi_org_v1_test_secret"

// fakeQwibi stands in for the Qwibi API with the behaviour the publisher
// relies on, written from the server's rules rather than from this package:
// it recomputes the content hash itself, replays an identical publication,
// refuses a changed one, requires each publication to advance the current
// one, and accepts App data only under a current release that declares it.
type fakeQwibi struct {
	pb.UnimplementedQwibiServiceServer
	pb.UnimplementedAppReleaseServiceServer
	pb.UnimplementedAppDataServiceServer

	mu       sync.Mutex
	hid      string
	appID    string
	public   bool
	releases []*pb.AppRelease // publication order
	objects  []*pb.ObjectWrite
	calls    []string
}

func (f *fakeQwibi) record(ctx context.Context, call string) (bearer, requestID string) {
	md, _ := metadata.FromIncomingContext(ctx)
	if v := md.Get("authorization"); len(v) > 0 {
		bearer = v[0]
	}
	if v := md.Get("x-request-id"); len(v) > 0 {
		requestID = v[0]
	}
	f.calls = append(f.calls, call)
	return bearer, requestID
}

// refusesKey is how Qwibi treats a bearer that carries an organization key it
// does not know: Unauthenticated on every method, public lookups included. A
// bearer that is not an organization key at all reads as anonymous there.
func refusesKey(bearer string) bool {
	return strings.HasPrefix(bearer, "Bearer "+organizationKeyPrefix) && bearer != "Bearer "+testKey
}

func (f *fakeQwibi) GetApp(ctx context.Context, req *pb.GetAppRequest) (*pb.GetAppResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	bearer, _ := f.record(ctx, "GetApp")
	if refusesKey(bearer) {
		return nil, status.Error(codes.Unauthenticated, "invalid token")
	}
	// A Public App's handle resolves for anyone; any other App only for its
	// organization's key.
	if req.GetHid() != f.hid || (!f.public && bearer != "Bearer "+testKey) {
		return nil, status.Error(codes.NotFound, "App not found")
	}
	return &pb.GetAppResponse{App: &pb.GeoApp{Uid: f.appID, Hid: f.hid}}, nil
}

func (f *fakeQwibi) PublishAppRelease(ctx context.Context, req *pb.PublishAppReleaseRequest) (*pb.PublishAppReleaseResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if bearer, _ := f.record(ctx, "PublishAppRelease"); bearer != "Bearer "+testKey {
		return nil, status.Error(codes.Unauthenticated, "bad key")
	}
	release := req.GetRelease()
	if release.GetAppId() != f.appID {
		return nil, status.Error(codes.PermissionDenied, "not this organization's App")
	}
	projection := proto.Clone(release).(*pb.AppRelease)
	projection.ReleaseId, projection.AppId, projection.SemanticVersion = "", "", ""
	projection.CanonicalContentSha256, projection.PublishedAt = nil, nil
	encoded, _ := proto.MarshalOptions{Deterministic: true}.Marshal(projection)
	sum := sha256.Sum256(encoded)
	if !bytes.Equal(sum[:], release.GetCanonicalContentSha256()) {
		return nil, status.Error(codes.InvalidArgument, "canonical content hash mismatch")
	}
	for _, stored := range f.releases {
		if stored.GetSemanticVersion() == release.GetSemanticVersion() {
			if proto.Equal(stored, release) {
				return &pb.PublishAppReleaseResponse{Release: stored}, nil
			}
			return nil, status.Error(codes.AlreadyExists, "App semantic version is already published with different immutable content")
		}
	}
	if n := len(f.releases); n > 0 {
		current := f.releases[n-1]
		if !release.GetPublishedAt().AsTime().After(current.GetPublishedAt().AsTime()) {
			return nil, status.Error(codes.FailedPrecondition, "published_at and release_id must advance the current App release")
		}
	}
	f.releases = append(f.releases, release)
	return &pb.PublishAppReleaseResponse{Release: release}, nil
}

func (f *fakeQwibi) ListAppReleases(ctx context.Context, req *pb.ListAppReleasesRequest) (*pb.ListAppReleasesResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if bearer, _ := f.record(ctx, "ListAppReleases"); bearer != "Bearer "+testKey {
		return nil, status.Error(codes.Unauthenticated, "bad key")
	}
	// Newest first and one per page, so the publisher must page and sort.
	start := 0
	if req.GetPage().GetCursor() != "" {
		start = int(req.GetPage().GetCursor()[0] - '0')
	}
	if start >= len(f.releases) {
		return &pb.ListAppReleasesResponse{Page: &pb.PageResponse{}}, nil
	}
	page := &pb.PageResponse{}
	if start+1 < len(f.releases) {
		page.NextCursor, page.HasMore = string(rune('0'+start+1)), true
	}
	return &pb.ListAppReleasesResponse{Releases: []*pb.AppRelease{f.releases[len(f.releases)-1-start]}, Page: page}, nil
}

func (f *fakeQwibi) ReplaceAppObjects(ctx context.Context, req *pb.ReplaceAppObjectsRequest) (*pb.ReplaceAppObjectsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if bearer, _ := f.record(ctx, "ReplaceAppObjects"); bearer != "Bearer "+testKey {
		return nil, status.Error(codes.Unauthenticated, "bad key")
	}
	if len(f.releases) == 0 {
		return nil, status.Error(codes.FailedPrecondition, "App has no current release")
	}
	declared := map[string]bool{}
	for _, s := range f.releases[len(f.releases)-1].GetObjectSchemas() {
		declared[s.GetObjectType()] = true
	}
	written := make([]*pb.GeoObject, 0, len(req.GetObjects()))
	for _, o := range req.GetObjects() {
		if !declared[o.GetObjectType()] {
			return nil, status.Errorf(codes.InvalidArgument, "object type %q is not declared", o.GetObjectType())
		}
		written = append(written, &pb.GeoObject{ObjectType: o.GetObjectType(), Properties: o.GetProperties()})
	}
	f.objects = req.GetObjects()
	return &pb.ReplaceAppObjectsResponse{Objects: written}, nil
}

func (f *fakeQwibi) ListAppObjects(ctx context.Context, req *pb.ListAppObjectsRequest) (*pb.ListAppObjectsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if bearer, _ := f.record(ctx, "ListAppObjects"); bearer != "Bearer "+testKey {
		return nil, status.Error(codes.Unauthenticated, "bad key")
	}
	out := make([]*pb.GeoObject, len(f.objects))
	for i, o := range f.objects {
		out[i] = &pb.GeoObject{ObjectType: o.GetObjectType(), Properties: o.GetProperties()}
	}
	return &pb.ListAppObjectsResponse{Objects: out, Page: &pb.PageResponse{}}, nil
}

func startFake(t *testing.T) (*fakeQwibi, *Publisher) {
	t.Helper()
	fake := &fakeQwibi{hid: "my-countries", appID: testApp, public: true}
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	pb.RegisterQwibiServiceServer(server, fake)
	pb.RegisterAppReleaseServiceServer(server, fake)
	pb.RegisterAppDataServiceServer(server, fake)
	go server.Serve(listener)
	t.Cleanup(server.Stop)
	publisher, err := NewPublisher("passthrough:///bufnet", testKey,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { publisher.Close() })
	return fake, publisher
}

func TestPublishFlow(t *testing.T) {
	fake, publisher := startFake(t)
	ctx := context.Background()

	appID, err := publisher.ResolveApp(ctx, "my-countries")
	if err != nil {
		t.Fatalf("resolve handle: %v", err)
	}
	if appID != testApp {
		t.Fatalf("resolved %s", appID)
	}
	if _, err := publisher.ResolveApp(ctx, "someone-elses"); err == nil || !strings.Contains(err.Error(), "no App with handle") {
		t.Fatalf("unknown handle: %v", err)
	}

	// The tutorial's first publication.
	fake.calls = nil
	result, err := publisher.Publish(ctx, appID, mustVersion(t, "1.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(fake.calls, ","); !strings.HasPrefix(got, "PublishAppRelease,") || !strings.HasSuffix(got, ",ReplaceAppObjects") {
		t.Fatalf("calls %s: the release must precede the data it declares", got)
	}
	if result.Objects != 177 || result.Current != "1.0.0" || result.ReleaseID != qwibi.ReleaseIDFor(appID, "1.0.0") {
		t.Fatalf("result %+v", result)
	}

	// Running the same command again changes nothing and fails nothing.
	if _, err := publisher.Publish(ctx, appID, mustVersion(t, "1.0.0")); err != nil {
		t.Fatalf("re-running a publication: %v", err)
	}
	if len(fake.releases) != 1 {
		t.Fatalf("%d releases stored after a replay", len(fake.releases))
	}

	// The compatible update becomes current with its data.
	result, err = publisher.Publish(ctx, appID, mustVersion(t, "1.1.0"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Current != "1.1.0" || result.DataSkipped {
		t.Fatalf("result %+v", result)
	}
	if fake.objects[0].GetProperties().GetFields()["subregion"].GetStringValue() == "" {
		t.Fatal("1.1.0 data was not written")
	}

	// 1.2.0 brings the visited mark in the release; the data stays the same.
	result, err = publisher.Publish(ctx, appID, mustVersion(t, "1.2.0"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Current != "1.2.0" || result.DataSkipped {
		t.Fatalf("result %+v", result)
	}
	current := fake.releases[len(fake.releases)-1]
	if len(current.GetMarks()) != 1 || current.GetMarks()[0].GetMarkedStyle() != VisitedMark.GetMarkedStyle() {
		t.Fatalf("1.2.0 was published with marks %v", current.GetMarks())
	}

	// An old version replays, but its data must not replace the newer data.
	result, err = publisher.Publish(ctx, appID, mustVersion(t, "1.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	if !result.DataSkipped || result.Current != "1.2.0" {
		t.Fatalf("result %+v", result)
	}
	if fake.objects[0].GetProperties().GetFields()["subregion"] == nil {
		t.Fatal("1.0.0 data overwrote the current version's data")
	}

	count, err := publisher.ObjectCount(ctx, appID)
	if err != nil || count != 177 {
		t.Fatalf("count %d, %v", count, err)
	}
}

func TestAPrivateAppResolvesWithTheOrganizationKey(t *testing.T) {
	fake, publisher := startFake(t)
	fake.public = false
	appID, err := publisher.ResolveApp(context.Background(), "my-countries")
	if err != nil || appID != testApp {
		t.Fatalf("resolved %q, %v", appID, err)
	}
	if got := strings.Join(fake.calls, ","); got != "GetApp,GetApp" {
		t.Fatalf("calls %s: the handle is tried without the key first", got)
	}
}

func TestPublishingChangedContentUnderAnOldVersionIsExplained(t *testing.T) {
	fake, publisher := startFake(t)
	ctx := context.Background()
	if _, err := publisher.Publish(ctx, testApp, mustVersion(t, "1.0.0")); err != nil {
		t.Fatal(err)
	}
	// Someone edited 1.0.0's declaration after publishing it.
	fake.releases[0] = proto.Clone(fake.releases[0]).(*pb.AppRelease)
	fake.releases[0].CanonicalContentSha256 = bytes.Repeat([]byte{1}, 32)
	_, err := publisher.Publish(ctx, testApp, mustVersion(t, "1.0.0"))
	if status.Code(err) != codes.AlreadyExists || !strings.Contains(err.Error(), "new version") {
		t.Fatalf("got %v, want AlreadyExists with the way out", err)
	}
}

func TestPublishNeedsAKeyAndTheKeyIsSent(t *testing.T) {
	if _, err := NewPublisher("passthrough:///bufnet", "", grpc.WithTransportCredentials(insecure.NewCredentials())); err == nil {
		t.Fatal("a publisher without a key was created")
	}
	fake, publisher := startFake(t)
	publisher.client.SetToken(organizationKeyPrefix + "wrong")
	_, err := publisher.Publish(context.Background(), testApp, mustVersion(t, "1.0.0"))
	if !errors.Is(err, ErrKeyNotAccepted) || len(fake.releases) != 0 {
		t.Fatalf("got %v with %d releases", err, len(fake.releases))
	}
}

// A wrong key must be named as the problem wherever it is first noticed, not
// reported as a missing App or a raw RPC error.
func TestAWrongKeyIsNamedByEveryCommand(t *testing.T) {
	for _, key := range []string{"fake", "qok_test_secret", "rg_v1_test_secret"} {
		if _, err := NewPublisher("passthrough:///bufnet", key, grpc.WithTransportCredentials(insecure.NewCredentials())); !errors.Is(err, ErrKeyNotAccepted) {
			t.Errorf("key %q without the organization key prefix: %v", key, err)
		}
	}

	fake, publisher := startFake(t)
	ctx := context.Background()
	if _, err := publisher.Publish(ctx, testApp, mustVersion(t, "1.0.0")); err != nil {
		t.Fatal(err)
	}
	publisher.client.SetToken(testKey[:len(testKey)-3]) // copied short
	// A Public App's handle resolves without the key; a private one and a
	// handle not public yet need the key, so the key is what gets named.
	fake.public = false
	for _, handle := range []string{"my-countries", "a-new-handle"} {
		if _, err := publisher.ResolveApp(ctx, handle); !errors.Is(err, ErrKeyNotAccepted) {
			t.Errorf("resolve %s: %v", handle, err)
		}
	}
	fake.public = true
	if _, err := publisher.Publish(ctx, testApp, mustVersion(t, "1.1.0")); !errors.Is(err, ErrKeyNotAccepted) {
		t.Errorf("publish: %v", err)
	}
	if _, err := publisher.Releases(ctx, testApp); !errors.Is(err, ErrKeyNotAccepted) {
		t.Errorf("status, releases: %v", err)
	}
	if _, err := publisher.ObjectCount(ctx, testApp); !errors.Is(err, ErrKeyNotAccepted) {
		t.Errorf("status, stored countries: %v", err)
	}
}
