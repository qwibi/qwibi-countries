package countries

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	qwibi "github.com/qwibi/qwibi-go-sdk"
	pb "github.com/qwibi/qwibi-proto-go/qwibi/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// CallTimeout bounds each call to Qwibi. Writing the 177 outlines is the
// largest one.
const CallTimeout = 30 * time.Second

var lowerUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// organizationKeyPrefix begins every organization key Qwibi issues.
const organizationKeyPrefix = "qwibi_org_v1_"

// ErrKeyNotAccepted reports an organization key Qwibi does not accept: most
// often one copied incompletely, else a revoked, expired or foreign key.
var ErrKeyNotAccepted = errors.New("the organization key was not accepted — check it was copied whole")

// callError explains a failed call: a key Qwibi refused to authenticate
// becomes ErrKeyNotAccepted, whatever the call; anything else keeps its cause.
func callError(what string, err error) error {
	if status.Code(err) == codes.Unauthenticated {
		return ErrKeyNotAccepted
	}
	return fmt.Errorf("%s: %w", what, err)
}

// Publisher publishes versions of this App with an organization key from
// developer settings. It never needs an App token: the App runs no process,
// so nothing acts as the App at run time.
type Publisher struct {
	client *qwibi.Client
}

// NewPublisher connects to a Qwibi endpoint ("host:port") with one SDK
// client. opts must carry the transport credentials (TLS for a real
// endpoint).
func NewPublisher(target, organizationKey string, opts ...grpc.DialOption) (*Publisher, error) {
	if organizationKey == "" {
		return nil, errors.New("an organization key is required")
	}
	// Qwibi reads a bearer without the key prefix as no key at all on its
	// public lookups, so a key missing its start would surface later as an
	// App that cannot be found. Refuse it here instead.
	if !strings.HasPrefix(organizationKey, organizationKeyPrefix) {
		return nil, fmt.Errorf("%w (an organization key starts with %s)", ErrKeyNotAccepted, organizationKeyPrefix)
	}
	client, err := qwibi.Dial(target, append(opts, qwibi.WithTimeout(CallTimeout))...)
	if err != nil {
		return nil, err
	}
	client.SetToken(organizationKey)
	return &Publisher{client: client}, nil
}

// Close releases the connection.
func (p *Publisher) Close() error { return p.client.Close() }

// ResolveApp turns an App handle (as shown in developer settings and in the
// App's link) or an App id into the App id.
//
// A Public App's handle is public, so it is looked up without credentials
// first. A Private or By-link App is visible only to its organization, so the
// lookup is repeated with the organization key.
func (p *Publisher) ResolveApp(ctx context.Context, app string) (string, error) {
	if lowerUUID.MatchString(app) {
		return app, nil
	}
	resp, err := p.client.Raw().GetApp(ctx, &pb.GetAppRequest{Hid: app})
	if err == nil {
		return resp.GetApp().GetUid(), nil
	}
	if status.Code(err) != codes.NotFound {
		return "", callError(fmt.Sprintf("look up App %q", app), err)
	}
	// Qwibi authenticates the key before it looks for the App, so a key it
	// does not accept fails here as ErrKeyNotAccepted, never as NotFound.
	found, err := p.client.GetAppByHid(ctx, app)
	if err != nil {
		switch status.Code(err) {
		case codes.NotFound, codes.PermissionDenied:
			return "", fmt.Errorf("no App with handle %q is visible to this organization key; check the handle, or pass the App id", app)
		}
		return "", callError(fmt.Sprintf("look up App %q", app), err)
	}
	return found.GetUid(), nil
}

// Result reports one publication.
type Result struct {
	ReleaseID string
	// Objects is the number of countries written; zero with DataSkipped.
	Objects int
	// DataSkipped is set when a later version is already current, so the
	// data of this older version must not replace it.
	DataSkipped bool
	// Current is the version Qwibi now serves.
	Current string
	// Kept is the number of countries whose object was updated in place and
	// kept its object id, and with it every mark people set on it.
	Kept int
	// Reissued names the countries (by handle) that were stored before but
	// came back under a new object id. Marks set on them were lost. It stays
	// empty against Qwibi, which updates an object in place by its handle.
	Reissued []string
}

// Publish publishes one version and then its App data.
//
// The release goes first: it declares the "country" type the data is
// written under. Both steps are safe to repeat. Publishing the same version
// again is a replay, because the SDK derives the release id from the App and
// the version and PublishedAt is fixed per version; the data write replaces
// the App's whole data set. So after any failure, run the same command again.
//
// Each country keeps its object across versions. Marks people set are keyed
// by object id, so a country written under a new id would lose them. The
// data write therefore never deletes and recreates: every object carries its
// country's handle (hid), and Qwibi updates the stored object with that
// handle in place, adds the handles it has not seen and removes only the
// handles a version no longer carries. Publish reads the stored ids first and
// reports in Result which countries kept theirs.
func (p *Publisher) Publish(ctx context.Context, appID string, v Version) (Result, error) {
	if err := checkVersion(appID, v); err != nil {
		return Result{}, fmt.Errorf("version %s does not pass the local check: %w", v.Semantic, err)
	}
	release, err := Release(appID, v)
	if err != nil {
		return Result{}, err
	}
	objects, err := Objects(v)
	if err != nil {
		return Result{}, err
	}

	resp, err := p.client.SealAndPublishAppRelease(ctx, appID, release)
	if err != nil {
		return Result{}, explainPublishError(v, err)
	}
	// A first publication and a replay both answer with this very release.
	want := qwibi.ReleaseIDFor(appID, v.Semantic)
	result := Result{ReleaseID: resp.GetRelease().GetReleaseId()}
	if result.ReleaseID != want {
		return result, fmt.Errorf("Qwibi answered with release %s, not %s", result.ReleaseID, want)
	}

	current, err := p.client.CurrentAppRelease(ctx, appID)
	if err != nil {
		return result, callError("read the current release", err)
	}
	if current == nil {
		return result, errors.New("the App has no current release right after a publication")
	}
	result.Current = current.GetSemanticVersion()
	if current.GetReleaseId() != want {
		result.DataSkipped = true
		return result, nil
	}

	stored, err := p.storedIDs(ctx, appID)
	if err != nil {
		return result, err
	}
	// Replace matches the objects by hid: kept handles are updated in place.
	written, err := p.client.ReplaceAppObjects(ctx, appID, objects)
	if err != nil {
		return result, callError("write App data", err)
	}
	result.Objects = len(written)
	for _, object := range written {
		before, ok := stored[object.GetHid()]
		switch {
		case !ok:
		case before == object.GetUid():
			result.Kept++
		default:
			result.Reissued = append(result.Reissued, object.GetHid())
		}
	}
	return result, nil
}

// storedIDs maps the handle of each stored object to its object id.
func (p *Publisher) storedIDs(ctx context.Context, appID string) (map[string]string, error) {
	objects, err := p.client.ListAllAppObjects(ctx, appID)
	if err != nil {
		return nil, callError("list App objects", err)
	}
	ids := make(map[string]string, len(objects))
	for _, object := range objects {
		if object.GetHid() != "" {
			ids[object.GetHid()] = object.GetUid()
		}
	}
	return ids, nil
}

// Releases lists the App's published releases in publication order, oldest
// first; the last one is current.
func (p *Publisher) Releases(ctx context.Context, appID string) ([]*pb.AppRelease, error) {
	var all []*pb.AppRelease
	cursor := ""
	for {
		resp, err := p.client.ListAppReleases(ctx, appID, &pb.PageRequest{Limit: 100, Cursor: cursor})
		if err != nil {
			return nil, callError("list releases", err)
		}
		all = append(all, resp.GetReleases()...)
		next := resp.GetPage().GetNextCursor()
		if next == "" {
			break
		}
		if next == cursor {
			return nil, errors.New("list releases: the cursor did not advance")
		}
		cursor = next
	}
	sort.SliceStable(all, func(i, j int) bool { return qwibi.LaterRelease(all[j], all[i]) })
	return all, nil
}

// ObjectCount counts the App's stored objects.
func (p *Publisher) ObjectCount(ctx context.Context, appID string) (int, error) {
	objects, err := p.client.ListAllAppObjects(ctx, appID)
	if err != nil {
		return 0, callError("list App objects", err)
	}
	return len(objects), nil
}

func explainPublishError(v Version, err error) error {
	switch status.Code(err) {
	case codes.AlreadyExists:
		return fmt.Errorf("version %s is already published with different content; published versions never change, so declare the change as a new version in Versions: %w", v.Semantic, err)
	case codes.FailedPrecondition:
		return fmt.Errorf("Qwibi refused version %s as incompatible with the current release or its stored data; run `qwibi-countries check` and see TUTORIAL.md, \"Ship a compatible update\": %w", v.Semantic, err)
	case codes.Unauthenticated:
		return ErrKeyNotAccepted
	case codes.PermissionDenied:
		return fmt.Errorf("the key may not publish this App: it must be a key of the App's organization: %w", err)
	}
	return fmt.Errorf("publish version %s: %w", v.Semantic, err)
}
