// Package countries is the "countries I have visited" Qwibi App: a release
// declaration and the App data Qwibi keeps for it.
//
// The App runs no process. Qwibi stores the country outlines, draws them on a
// person's map once they add the App to My map, and keeps each person's
// "visited" marks itself. Publishing a version is the only thing the
// developer ever does; see TUTORIAL.md.
package countries

import (
	"fmt"
	"time"

	qwibi "github.com/qwibi/qwibi-go-sdk"
	pb "github.com/qwibi/qwibi-proto-go/qwibi/v1"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ObjectType is the one kind of object this App publishes.
const ObjectType = "country"

// Version is one published version of the App. Every field is part of the
// release, so a published Version never changes: a change is a new Version
// appended to Versions.
type Version struct {
	// Semantic is the App's SemVer version.
	Semantic string
	// PublishedAt is fixed per version, not taken from the clock, so that
	// publishing the same version again sends byte-identical content and Qwibi
	// answers it as a replay instead of a conflict. Each version must be later
	// than the one before it.
	PublishedAt time.Time
	// Subregion adds the optional "subregion" property and its card row.
	Subregion bool
	// Russian adds a Russian localization.
	Russian bool
	// Marks declares the "visited" mark (marks.go).
	Marks bool
}

// Versions lists every version in publication order. The last one is the
// current version. Qwibi refuses a publication that would break stored data
// or the previous declaration, and CheckCompatible (compat.go) runs the same
// rules locally before anything is sent.
var Versions = []Version{
	{
		Semantic:    "1.0.0",
		PublishedAt: time.Date(2026, time.September, 26, 12, 0, 0, 0, time.UTC),
	},
	{
		// The compatible update of TUTORIAL.md step 6: one optional property,
		// one more card row and a second language. Nothing required is added
		// and nothing is removed, so every existing installation and every
		// person's marks carry over without anyone doing anything.
		Semantic:    "1.1.0",
		PublishedAt: time.Date(2026, time.October, 3, 12, 0, 0, 0, time.UTC),
		Subregion:   true,
		Russian:     true,
	},
	{
		// People mark the countries they have visited. Declaring a mark
		// changes no stored data, so this too is a compatible update: every
		// person who added the App gets the "Visited" toggle on each country
		// without doing anything.
		Semantic:    "1.2.0",
		PublishedAt: time.Date(2026, time.October, 10, 12, 0, 0, 0, time.UTC),
		Subregion:   true,
		Russian:     true,
		Marks:       true,
	},
}

// Latest is the current version.
func Latest() Version { return Versions[len(Versions)-1] }

// FindVersion returns the declared version with that SemVer string.
func FindVersion(semantic string) (Version, error) {
	for _, v := range Versions {
		if v.Semantic == semantic {
			return v, nil
		}
	}
	return Version{}, fmt.Errorf("version %q is not declared in Versions", semantic)
}

// Qwibi contract versions this App is written against. The platform serves
// one contract version and refuses a release whose range excludes it.
const (
	ContractMinimum = "0.1.0"
	ContractMaximum = "1.0.0"
)

// CountryStyle draws every outline on the map. It is App data, so every
// person sees the same outlines; whether a person has visited a country is
// that person's mark and is accented with VisitedMark.MarkedStyle.
var CountryStyle = qwibi.Style{
	Fill:        "#94A3B8",
	FillOpacity: qwibi.StyleNumber(0.12),
	Stroke:      "#64748B",
	StrokeWidth: qwibi.StyleNumber(0.8),
}

// Release builds the declaration of one version for one App. The SDK's
// qwibi.SealRelease fills in the release id and the content hash; see
// SealedRelease.
func Release(appID string, v Version) (*pb.AppRelease, error) {
	schema, err := objectSchema(v)
	if err != nil {
		return nil, err
	}
	release := &pb.AppRelease{
		AppId:           appID,
		SemanticVersion: v.Semantic,
		ContractRange: &pb.QwibiContractRange{
			MinimumInclusive: ContractMinimum,
			MaximumExclusive: ContractMaximum,
		},
		ObjectSchemas: []*pb.AppObjectSchema{{ObjectType: ObjectType, Schema: schema}},
		ObjectPresentations: []*pb.AppObjectPresentation{{
			ObjectType:                   ObjectType,
			TitleProperty:                "name",
			SubtitleProperty:             "continent",
			PropertyRows:                 propertyRows(v),
			SingularLabelLocalizationKey: "country.singular",
			PluralLabelLocalizationKey:   "country.plural",
		}},
		Localizations: localizations(v),
		SafeFallbacks: &pb.SafeGenericFallbacks{
			UnknownObject: pb.UnknownObjectFallback_UNKNOWN_OBJECT_FALLBACK_GENERIC_PROPERTIES,
			UnknownUi:     pb.UnknownUiFallback_UNKNOWN_UI_FALLBACK_OMIT,
			UnknownAction: pb.UnknownActionFallback_UNKNOWN_ACTION_FALLBACK_DISABLED,
		},
		PublisherMetadata: &pb.ReleasePublisherMetadata{
			PublisherName:    PublisherName,
			SupportUrl:       SupportURL,
			PrivacyPolicyUrl: PrivacyPolicyURL,
			LicenseName:      LicenseName,
			LicenseUrl:       LicenseURL,
		},
		PublishedAt: timestamppb.New(v.PublishedAt),
		Marks:       marks(v),
	}
	return release, nil
}

// SealedRelease is Release with its release id and content hash filled in by
// the SDK, ready to publish. The release id is derived from the App and the
// version, and PublishedAt is fixed per version, so sealing the same version
// again gives the same bytes and Qwibi answers a repeated publication as a
// replay.
func SealedRelease(appID string, v Version) (*pb.AppRelease, error) {
	release, err := Release(appID, v)
	if err != nil {
		return nil, err
	}
	return qwibi.SealRelease(appID, release)
}

// objectSchema is the JSON Schema of a country's properties.
//
// additionalProperties is false from the first version on purpose. Qwibi lets
// a later version add a property only where the earlier schema did not admit
// that key (and no stored object carries it). With the key admitted
// implicitly, adding "subregion" in 1.1.0 would count as narrowing "any
// value" to "a string" and be refused.
func objectSchema(v Version) (*structpb.Struct, error) {
	text := func() map[string]any { return map[string]any{"type": "string", "minLength": 1} }
	properties := map[string]any{
		"code":      text(),
		"iso_a2":    map[string]any{"type": "string", "pattern": "^[A-Z]{2}$"},
		"name":      text(),
		"continent": text(),
	}
	if v.Subregion {
		properties["subregion"] = text()
	}
	return structpb.NewStruct(map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             []any{"code", "name", "continent"},
		"additionalProperties": false,
	})
}

func propertyRows(v Version) []*pb.AppObjectPropertyRow {
	row := func(property string) *pb.AppObjectPropertyRow {
		return &pb.AppObjectPropertyRow{
			PropertyId:           property,
			LabelLocalizationKey: "country." + property,
			FormatKind:           pb.DeclarativeValueFormatKind_DECLARATIVE_VALUE_FORMAT_KIND_TEXT,
		}
	}
	rows := []*pb.AppObjectPropertyRow{row("code"), row("iso_a2")}
	if v.Subregion {
		rows = append(rows, row("subregion"))
	}
	return rows
}

func localizations(v Version) []*pb.AppLocalization {
	english := map[string]string{
		"country.singular":               "Country",
		"country.plural":                 "Countries",
		"country.code":                   "Code",
		"country.iso_a2":                 "ISO code",
		"country.continent":              "Continent",
		VisitedMark.LabelLocalizationKey: "Visited",
		visitedCountLabelKey:             "Countries visited",
	}
	result := []*pb.AppLocalization{{Locale: "en", Entries: english}}
	if v.Subregion {
		english["country.subregion"] = "Region"
	}
	if v.Russian {
		russian := map[string]string{
			"country.singular":               "Страна",
			"country.plural":                 "Страны",
			"country.code":                   "Код",
			"country.iso_a2":                 "Код ISO",
			"country.continent":              "Континент",
			VisitedMark.LabelLocalizationKey: "Была здесь",
			visitedCountLabelKey:             "Посещено стран",
		}
		if v.Subregion {
			russian["country.subregion"] = "Регион"
		}
		if v.Marks {
			// The toggle becomes visible with the mark, so from 1.2.0 on it
			// reads the same for every person ("the country is visited").
			russian[VisitedMark.LabelLocalizationKey] = "Посещена"
		}
		result = append(result, &pb.AppLocalization{Locale: "ru", Entries: russian})
	}
	return result
}
