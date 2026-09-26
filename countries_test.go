package countries

import (
	"bytes"
	"encoding/hex"
	"regexp"
	"strings"
	"testing"
	"time"

	qwibi "github.com/qwibi/qwibi-go-sdk"
	pb "github.com/qwibi/qwibi-proto-go/qwibi/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const testApp = "0190f5a2-7c1e-7d4b-9a3f-5e2d8c1b4a60"

func TestDatasetIsCompleteAndAddressable(t *testing.T) {
	all, err := Countries()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 177 {
		t.Fatalf("got %d countries, want the 177 of Natural Earth 1:110m", len(all))
	}
	codes := map[string]Country{}
	for _, c := range all {
		if _, dup := codes[c.Code]; dup {
			t.Errorf("code %s repeats: two countries would share one object", c.Code)
		}
		codes[c.Code] = c
		if c.Name == "" || c.Continent == "" {
			t.Errorf("%s lacks a name or continent", c.Code)
		}
		if c.ISOA2 != "" && !regexp.MustCompile(`^[A-Z]{2}$`).MatchString(c.ISOA2) {
			t.Errorf("%s has ISO code %q", c.Code, c.ISOA2)
		}
		// The SDK's own builder checks the HID grammar, the reserved root
		// names and every ring (closed, at least four positions, in range).
		for i, polygon := range c.polygons {
			if _, err := qwibi.Zone(polygon, qwibi.L1ObjectOptions{Hid: c.HID()}); err != nil {
				t.Errorf("%s polygon %d: %v", c.Code, i, err)
			}
		}
		if c.Geometry == nil || len(c.polygons) == 0 {
			t.Errorf("%s has no outline", c.Code)
		}
	}
	for code, name := range map[string]string{"FRA": "France", "JPN": "Japan", "BRA": "Brazil", "RUS": "Russia", "NZL": "New Zealand"} {
		if codes[code].Name != name {
			t.Errorf("%s is %q, want %q", code, codes[code].Name, name)
		}
	}
	// France is metropolitan France plus French Guiana: a MultiPolygon must
	// survive parsing as more than one polygon.
	if len(codes["FRA"].polygons) < 2 {
		t.Errorf("France has %d polygons, want the MultiPolygon kept", len(codes["FRA"].polygons))
	}
}

func TestEveryVersionPassesTheLocalCheck(t *testing.T) {
	if err := Check(testApp); err != nil {
		t.Fatal(err)
	}
}

func TestObjectsCarryOnlyWhatTheirVersionDeclares(t *testing.T) {
	v1, v11 := mustVersion(t, "1.0.0"), mustVersion(t, "1.1.0")
	first, err := Objects(v1)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range first {
		if _, ok := o.GetProperties().GetFields()["subregion"]; ok {
			t.Fatalf("1.0.0 object %s carries subregion; 1.1.0 could then not declare it", o.GetHid())
		}
	}
	second, err := Objects(v11)
	if err != nil {
		t.Fatal(err)
	}
	withSubregion := 0
	for _, o := range second {
		if o.GetProperties().GetFields()["subregion"].GetStringValue() != "" {
			withSubregion++
		}
		if o.GetStyle() == "" || o.GetObjectType() != ObjectType || o.GetName() == "" {
			t.Fatalf("object %s is incomplete: %v", o.GetHid(), o)
		}
	}
	if withSubregion != len(second) {
		t.Errorf("%d of %d 1.1.0 objects have a subregion", withSubregion, len(second))
	}
}

func TestCheckObjectRejectsDataTheSchemaDoesNotAdmit(t *testing.T) {
	release, err := Release(testApp, mustVersion(t, "1.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	schema := release.GetObjectSchemas()[0].GetSchema()
	valid := func() *pb.ObjectWrite {
		props, _ := structpb.NewStruct(map[string]any{"code": "FRA", "name": "France", "continent": "Europe", "iso_a2": "FR"})
		return &pb.ObjectWrite{Hid: "fra", ObjectType: ObjectType, Properties: props}
	}
	if err := CheckObject(schema, valid()); err != nil {
		t.Fatalf("valid object refused: %v", err)
	}
	for name, mutate := range map[string]func(*pb.ObjectWrite){
		"undeclared key":   func(o *pb.ObjectWrite) { o.Properties.Fields["subregion"] = structpb.NewStringValue("Western Europe") },
		"missing required": func(o *pb.ObjectWrite) { delete(o.Properties.Fields, "continent") },
		"not a string":     func(o *pb.ObjectWrite) { o.Properties.Fields["name"] = structpb.NewNumberValue(1) },
		"empty":            func(o *pb.ObjectWrite) { o.Properties.Fields["name"] = structpb.NewStringValue("") },
		"bad ISO code":     func(o *pb.ObjectWrite) { o.Properties.Fields["iso_a2"] = structpb.NewStringValue("fr") },
		"wrong type":       func(o *pb.ObjectWrite) { o.ObjectType = "place" },
	} {
		o := valid()
		mutate(o)
		if CheckObject(schema, o) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestReleaseRulesCatchDeclarationMistakes(t *testing.T) {
	for name, mutate := range map[string]func(*pb.AppRelease){
		"title not in schema":   func(r *pb.AppRelease) { r.ObjectPresentations[0].TitleProperty = "title" },
		"row not in schema":     func(r *pb.AppRelease) { r.ObjectPresentations[0].PropertyRows[0].PropertyId = "population" },
		"label without English": func(r *pb.AppRelease) { delete(r.Localizations[0].Entries, "country.code") },
		"plain HTTP link":       func(r *pb.AppRelease) { r.PublisherMetadata.SupportUrl = "http://example.com" },
		"empty contract range":  func(r *pb.AppRelease) { r.ContractRange.MaximumExclusive = r.ContractRange.MinimumInclusive },
		"unknown-UI fallback": func(r *pb.AppRelease) {
			r.SafeFallbacks.UnknownUi = pb.UnknownUiFallback_UNKNOWN_UI_FALLBACK_UNSPECIFIED
		},
		"duplicate locale":        func(r *pb.AppRelease) { r.Localizations = append(r.Localizations, r.Localizations[0]) },
		"presentation of unknown": func(r *pb.AppRelease) { r.ObjectPresentations[0].ObjectType = "city" },
	} {
		release, err := Release(testApp, Latest())
		if err != nil {
			t.Fatal(err)
		}
		mutate(release)
		if checkReleaseRules(release) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestTheVisitedMarkArrivesWithVersion120(t *testing.T) {
	for _, v := range Versions {
		release, err := Release(testApp, v)
		if err != nil {
			t.Fatal(err)
		}
		marks := release.GetMarks()
		if !v.Marks {
			if len(marks) != 0 {
				t.Errorf("%s declares marks; a published version must keep its bytes", v.Semantic)
			}
			continue
		}
		if len(marks) != 1 || !proto.Equal(marks[0], VisitedMark) {
			t.Fatalf("%s marks %v", v.Semantic, marks)
		}
		// A traveller marks many countries: single would keep one.
		if marks[0].GetSingle() || !marks[0].GetShowCount() {
			t.Errorf("%s: single %v, show_count %v", v.Semantic, marks[0].GetSingle(), marks[0].GetShowCount())
		}
	}
	if !Latest().Marks {
		t.Error("the latest version has no visited mark")
	}
}

func TestMarkRulesCatchDeclarationMistakes(t *testing.T) {
	release, err := Release(testApp, Latest())
	if err != nil {
		t.Fatal(err)
	}
	if err := checkMarks(release); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*pb.AppRelease){
		"unknown object type": func(r *pb.AppRelease) { r.Marks[0].ObjectType = "city" },
		"untranslated label":  func(r *pb.AppRelease) { r.Marks[0].LabelLocalizationKey = "mark.been" },
		"bad id":              func(r *pb.AppRelease) { r.Marks[0].MarkId = "Visited!" },
		"style not a colour":  func(r *pb.AppRelease) { r.Marks[0].MarkedStyle = `{"fill":"#16A34A"}` },
		"no style":            func(r *pb.AppRelease) { r.Marks[0].MarkedStyle = "" },
		"half a filter":       func(r *pb.AppRelease) { r.Marks[0].FilterProperty = "country" },
		"declared twice":      func(r *pb.AppRelease) { r.Marks = append(r.Marks, proto.Clone(r.Marks[0]).(*pb.AppMarkDefinition)) },
	} {
		broken := proto.Clone(release).(*pb.AppRelease)
		mutate(broken)
		if checkMarks(broken) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestContractFieldRulesAreApplied(t *testing.T) {
	// protovalidate runs the published contract's own rules, so a release
	// the server's first gate refuses fails here too.
	if _, err := SealedRelease("0190F5A2-7C1E-7D4B-9A3F-5E2D8C1B4A60", Latest()); err == nil {
		t.Error("an upper-case App id was accepted")
	}
	v := Latest()
	v.Semantic = "1.1"
	if err := checkVersion(testApp, v); err == nil {
		t.Error("a non-SemVer version was accepted")
	}
	v = Latest()
	v.PublishedAt = v.PublishedAt.Add(time.Nanosecond)
	if err := checkVersion(testApp, v); err == nil {
		t.Error("a nanosecond publication time was accepted")
	}
}

func TestContentHashCoversContentOnly(t *testing.T) {
	a, err := SealedRelease(testApp, Latest())
	if err != nil {
		t.Fatal(err)
	}
	b := proto.Clone(a).(*pb.AppRelease)
	b.AppId = "0190f5a2-7c1e-7d4b-9a3f-000000000000"
	b.ReleaseId = "0190f5a2-7c1e-7d4b-9a3f-111111111111"
	b.SemanticVersion = "9.9.9"
	b.PublishedAt = timestamppb.New(time.Unix(0, 0))
	sumA, _ := qwibi.ReleaseContentSHA256(a)
	sumB, _ := qwibi.ReleaseContentSHA256(b)
	if sumA != sumB {
		t.Error("identity fields changed the content hash")
	}
	if !bytes.Equal(a.GetCanonicalContentSha256(), sumA[:]) {
		t.Error("SealedRelease did not store the content hash")
	}
	b.Localizations[0].Entries["country.code"] = "Country code"
	if sumC, _ := qwibi.ReleaseContentSHA256(b); sumC == sumA {
		t.Error("a label change did not change the content hash")
	}
	first, _ := SealedRelease(testApp, mustVersion(t, "1.0.0"))
	if bytes.Equal(first.GetCanonicalContentSha256(), a.GetCanonicalContentSha256()) {
		t.Error("1.0.0 and 1.1.0 have the same content")
	}
	// Map iteration order must not leak into the encoding.
	for i := 0; i < 20; i++ {
		again, _ := SealedRelease(testApp, Latest())
		if !proto.Equal(again, a) {
			t.Fatal("sealing the same version twice gave different releases; a re-run would conflict")
		}
	}
}

// publishedContent pins the content hash (the first 16 hex digits, as
// `qwibi-countries check` prints them) of every version already released:
// here all three declared versions. A released version never changes, so
// neither may its hash: a change to what an old version builds must become a
// new version instead. When you make the App yours (publisher.go), empty this
// map, and add each version here once you have published it.
var publishedContent = map[string]string{
	"1.0.0": "1d5af79874716c90",
	"1.1.0": "407f9671c6474265",
	"1.2.0": "748838e082a081c7",
}

func TestPublishedVersionsKeepTheirContent(t *testing.T) {
	for semantic, want := range publishedContent {
		release, err := SealedRelease(testApp, mustVersion(t, semantic))
		if err != nil {
			t.Fatal(err)
		}
		if got := hex.EncodeToString(release.GetCanonicalContentSha256())[:16]; got != want {
			t.Errorf("%s now builds content %s, published as %s; declare the change as a new version", semantic, got, want)
		}
	}
}

func TestVersionsMoveForward(t *testing.T) {
	seen := map[string]bool{}
	for i, v := range Versions {
		if seen[v.Semantic] {
			t.Errorf("version %s declared twice", v.Semantic)
		}
		seen[v.Semantic] = true
		if i > 0 && (compareSemVer(Versions[i-1].Semantic, v.Semantic) >= 0 || !v.PublishedAt.After(Versions[i-1].PublishedAt)) {
			t.Errorf("version %s does not follow %s", v.Semantic, Versions[i-1].Semantic)
		}
	}
	if _, err := FindVersion("2.0.0"); err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Errorf("FindVersion of an undeclared version: %v", err)
	}
}

func mustVersion(t *testing.T, semantic string) Version {
	t.Helper()
	v, err := FindVersion(semantic)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
