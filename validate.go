package countries

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"buf.build/go/protovalidate"
	pb "github.com/qwibi/qwibi-proto-go/qwibi/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

// Check runs, locally and without a network, the checks Qwibi applies when a
// version is published, and returns every problem found:
//
//   - the field rules of the published contract (protovalidate) on the exact
//     requests the publisher sends;
//   - the release rules this App's declaration can break (contract range,
//     fallbacks, HTTPS links, presentation bindings, English labels, marks);
//   - that every object carries only what its version's schema declares
//     (Qwibi stores App data without checking it against the schema, and the
//     next version's compatibility depends on it);
//   - that Versions only ever move forward compatibly (compat.go).
//
// A clean Check is not a promise that Qwibi accepts the publication; it
// catches the mistakes that are this App's to make before they cost a round
// trip.
func Check(appID string) error {
	var problems []error
	for i, v := range Versions {
		if err := checkVersion(appID, v); err != nil {
			problems = append(problems, fmt.Errorf("version %s: %w", v.Semantic, err))
		}
		if i == 0 {
			continue
		}
		prev := Versions[i-1]
		if compareSemVer(prev.Semantic, v.Semantic) >= 0 {
			problems = append(problems, fmt.Errorf("version %s must be greater than %s", v.Semantic, prev.Semantic))
		}
		if !v.PublishedAt.After(prev.PublishedAt) {
			problems = append(problems, fmt.Errorf("version %s PublishedAt must be later than %s's", v.Semantic, prev.Semantic))
		}
		prevRelease, err1 := Release(appID, prev)
		nextRelease, err2 := Release(appID, v)
		prevObjects, err3 := Objects(prev)
		if err := errors.Join(err1, err2, err3); err != nil {
			problems = append(problems, err)
			continue
		}
		if err := CheckCompatible(prevRelease, nextRelease, prevObjects); err != nil {
			problems = append(problems, fmt.Errorf("%s -> %s: %w", prev.Semantic, v.Semantic, err))
		}
	}
	return errors.Join(problems...)
}

func checkVersion(appID string, v Version) error {
	if v.PublishedAt.Nanosecond()%1000 != 0 {
		return errors.New("PublishedAt must not be more precise than a microsecond")
	}
	release, err := SealedRelease(appID, v)
	if err != nil {
		return err
	}
	objects, err := Objects(v)
	if err != nil {
		return err
	}
	var problems []error
	if err := protovalidate.Validate(&pb.PublishAppReleaseRequest{Release: release}); err != nil {
		problems = append(problems, err)
	}
	if err := protovalidate.Validate(&pb.ReplaceAppObjectsRequest{AppId: appID, Objects: objects}); err != nil {
		problems = append(problems, err)
	}
	if err := checkReleaseRules(release); err != nil {
		problems = append(problems, err)
	}
	if err := checkMarks(release); err != nil {
		problems = append(problems, err)
	}
	schema := release.GetObjectSchemas()[0].GetSchema()
	for _, object := range objects {
		if err := CheckObject(schema, object); err != nil {
			problems = append(problems, err)
		}
	}
	return errors.Join(problems...)
}

// checkReleaseRules is the part of Qwibi's publication rules that this
// declaration can break. Rules about actions, UI contributions and assets
// are left out: this App declares none. Marks are checked by checkMarks.
func checkReleaseRules(release *pb.AppRelease) error {
	var problems []error
	fail := func(format string, args ...any) { problems = append(problems, fmt.Errorf(format, args...)) }

	for name, version := range map[string]string{
		"semantic_version":                 release.GetSemanticVersion(),
		"contract_range.minimum_inclusive": release.GetContractRange().GetMinimumInclusive(),
		"contract_range.maximum_exclusive": release.GetContractRange().GetMaximumExclusive(),
	} {
		if !semVer.MatchString(version) {
			fail("%s %q is not SemVer MAJOR.MINOR.PATCH", name, version)
		}
	}
	if compareSemVer(release.GetContractRange().GetMinimumInclusive(), release.GetContractRange().GetMaximumExclusive()) >= 0 {
		fail("contract_range must be non-empty and increasing")
	}
	fallbacks := release.GetSafeFallbacks()
	if o := fallbacks.GetUnknownObject(); o != pb.UnknownObjectFallback_UNKNOWN_OBJECT_FALLBACK_GENERIC_PROPERTIES &&
		o != pb.UnknownObjectFallback_UNKNOWN_OBJECT_FALLBACK_HIDDEN {
		fail("unknown_object fallback must be GENERIC_PROPERTIES or HIDDEN")
	}
	if fallbacks.GetUnknownUi() != pb.UnknownUiFallback_UNKNOWN_UI_FALLBACK_OMIT {
		fail("unknown_ui fallback must be OMIT")
	}
	if fallbacks.GetUnknownAction() != pb.UnknownActionFallback_UNKNOWN_ACTION_FALLBACK_DISABLED {
		fail("unknown_action fallback must be DISABLED")
	}
	meta := release.GetPublisherMetadata()
	for name, raw := range map[string]string{
		"support_url":        meta.GetSupportUrl(),
		"privacy_policy_url": meta.GetPrivacyPolicyUrl(),
		"license_url":        meta.GetLicenseUrl(),
	} {
		if raw == "" {
			continue
		}
		if u, err := url.Parse(raw); err != nil || u.Scheme != "https" || u.Host == "" {
			fail("%s %q must be an absolute HTTPS URL", name, raw)
		}
	}

	schemas := map[string]*structpb.Struct{}
	for _, s := range release.GetObjectSchemas() {
		if _, dup := schemas[s.GetObjectType()]; dup {
			fail("duplicate object schema %q", s.GetObjectType())
		}
		schemas[s.GetObjectType()] = s.GetSchema()
	}
	required := map[string]string{} // localization key -> where it is used
	for _, p := range release.GetObjectPresentations() {
		schema, ok := schemas[p.GetObjectType()]
		if !ok {
			fail("presentation references unknown object schema %q", p.GetObjectType())
			continue
		}
		for slot, property := range map[string]string{"title": p.GetTitleProperty(), "subtitle": p.GetSubtitleProperty()} {
			if property != "" && schemaProperty(schema, property) == nil {
				fail("presentation %s property %q is not declared in the schema", slot, property)
			}
		}
		required[p.GetSingularLabelLocalizationKey()] = "singular label"
		required[p.GetPluralLabelLocalizationKey()] = "plural label"
		seen := map[string]bool{}
		for _, row := range p.GetPropertyRows() {
			if seen[row.GetPropertyId()] {
				fail("duplicate property row %q", row.GetPropertyId())
			}
			seen[row.GetPropertyId()] = true
			property := schemaProperty(schema, row.GetPropertyId())
			if property == nil {
				fail("property row %q is not declared in the schema", row.GetPropertyId())
			} else if row.GetFormatKind() == pb.DeclarativeValueFormatKind_DECLARATIVE_VALUE_FORMAT_KIND_TEXT &&
				property.GetFields()["type"].GetStringValue() != "string" {
				fail("property row %q is TEXT but its schema type is not string", row.GetPropertyId())
			}
			required[row.GetLabelLocalizationKey()] = "property row " + row.GetPropertyId()
		}
	}
	english := map[string]string(nil)
	locales := map[string]bool{}
	for _, l := range release.GetLocalizations() {
		if locales[l.GetLocale()] {
			fail("duplicate localization %q", l.GetLocale())
		}
		locales[l.GetLocale()] = true
		if l.GetLocale() == "en" {
			english = l.GetEntries()
		}
	}
	keys := make([]string, 0, len(required))
	for key := range required {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if key == "" || english[key] == "" {
			fail("%s uses localization key %q with no English text", required[key], key)
		}
	}
	return errors.Join(problems...)
}

// markID is the contract's rule for a mark id.
var markID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// literalColour is the form of a literal marked_style colour.
var literalColour = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// checkMarks checks the marks a release declares the way Qwibi does at
// publication, and one rule of this App's own: the accent is a literal colour
// (the contract also admits a host's semantic colour token).
func checkMarks(release *pb.AppRelease) error {
	var problems []error
	schemas := map[string]bool{}
	for _, s := range release.GetObjectSchemas() {
		schemas[s.GetObjectType()] = true
	}
	seen := map[string]bool{}
	for _, mark := range release.GetMarks() {
		id := mark.GetMarkId()
		if !markID.MatchString(id) {
			problems = append(problems, fmt.Errorf("mark id %q is not a lower-case identifier", id))
		}
		if seen[id] {
			problems = append(problems, fmt.Errorf("mark %q is declared twice", id))
		}
		seen[id] = true
		if !schemas[mark.GetObjectType()] {
			problems = append(problems, fmt.Errorf("mark %q applies to undeclared object type %q", id, mark.GetObjectType()))
		}
		if !literalColour.MatchString(mark.GetMarkedStyle()) {
			problems = append(problems, fmt.Errorf("mark %q marked_style %q is not a #RRGGBB colour", id, mark.GetMarkedStyle()))
		}
		if (mark.GetFiltersObjectType() == "") != (mark.GetFilterProperty() == "") {
			problems = append(problems, fmt.Errorf("mark %q sets only one of filters_object_type and filter_property", id))
		}
		for _, l := range release.GetLocalizations() {
			if l.GetEntries()[mark.GetLabelLocalizationKey()] == "" {
				problems = append(problems, fmt.Errorf("mark %q label %q has no %s text", id, mark.GetLabelLocalizationKey(), l.GetLocale()))
			}
		}
	}
	return errors.Join(problems...)
}

// CheckObject checks one object against the schema subset this App uses:
// an object with string properties, required keys and no undeclared keys.
func CheckObject(schema *structpb.Struct, object *pb.ObjectWrite) error {
	props := object.GetProperties().GetFields()
	declared := schema.GetFields()["properties"].GetStructValue().GetFields()
	var problems []error
	for _, key := range schema.GetFields()["required"].GetListValue().GetValues() {
		if _, ok := props[key.GetStringValue()]; !ok {
			problems = append(problems, fmt.Errorf("object %s lacks required %q", object.GetHid(), key.GetStringValue()))
		}
	}
	keys := make([]string, 0, len(props))
	for key := range props {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		property, ok := declared[key]
		if !ok {
			problems = append(problems, fmt.Errorf("object %s carries undeclared %q", object.GetHid(), key))
			continue
		}
		s := property.GetStructValue().GetFields()
		value, isString := props[key].GetKind().(*structpb.Value_StringValue)
		if s["type"].GetStringValue() == "string" {
			if !isString {
				problems = append(problems, fmt.Errorf("object %s %q is not a string", object.GetHid(), key))
				continue
			}
			if min, ok := s["minLength"]; ok && float64(len([]rune(value.StringValue))) < min.GetNumberValue() {
				problems = append(problems, fmt.Errorf("object %s %q is empty", object.GetHid(), key))
			}
			if pattern, ok := s["pattern"]; ok && !regexp.MustCompile(pattern.GetStringValue()).MatchString(value.StringValue) {
				problems = append(problems, fmt.Errorf("object %s %q=%q does not match %s", object.GetHid(), key, value.StringValue, pattern.GetStringValue()))
			}
		}
	}
	if object.GetObjectType() != ObjectType {
		problems = append(problems, fmt.Errorf("object %s has type %q", object.GetHid(), object.GetObjectType()))
	}
	return errors.Join(problems...)
}

func schemaProperty(schema *structpb.Struct, name string) *structpb.Struct {
	return schema.GetFields()["properties"].GetStructValue().GetFields()[name].GetStructValue()
}

var semVer = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// compareSemVer orders plain MAJOR.MINOR.PATCH versions; this App uses no
// pre-release versions.
func compareSemVer(a, b string) int {
	left, right := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 3 && i < len(left) && i < len(right); i++ {
		x, _ := strconv.Atoi(left[i])
		y, _ := strconv.Atoi(right[i])
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return len(left) - len(right)
}
