package countries

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	qwibi "github.com/qwibi/qwibi-go-sdk"
	pb "github.com/qwibi/qwibi-proto-go/qwibi/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

// countriesGeoJSON is Natural Earth 1:110m admin-0 (public domain), trimmed by
// tools/naturalearth.
//
//go:embed data/countries.geojson
var countriesGeoJSON []byte

// Country is one outline of the embedded dataset.
type Country struct {
	Code      string // Natural Earth ADM0_A3, unique; the object HID is its lower case
	ISOA2     string // ISO 3166-1 alpha-2; empty where none is assigned
	Name      string
	Continent string
	Subregion string
	Geometry  *pb.Geometry
	polygons  [][][][2]float64 // the same outline, as rings of positions
}

// HID is the country's stable address inside the App's data.
func (c Country) HID() string { return strings.ToLower(c.Code) }

// Countries parses the embedded dataset.
func Countries() ([]Country, error) {
	var collection struct {
		Features []struct {
			Properties struct {
				Code      string `json:"code"`
				ISOA2     string `json:"iso_a2"`
				Name      string `json:"name"`
				Continent string `json:"continent"`
				Subregion string `json:"subregion"`
			} `json:"properties"`
			Geometry struct {
				Type        string          `json:"type"`
				Coordinates json.RawMessage `json:"coordinates"`
			} `json:"geometry"`
		} `json:"features"`
	}
	if err := json.Unmarshal(countriesGeoJSON, &collection); err != nil {
		return nil, fmt.Errorf("parse embedded countries: %w", err)
	}
	result := make([]Country, 0, len(collection.Features))
	for _, feature := range collection.Features {
		p := feature.Properties
		var geometry *pb.Geometry
		var polygons [][][][2]float64
		switch feature.Geometry.Type {
		case "Polygon":
			var rings [][][2]float64
			if err := json.Unmarshal(feature.Geometry.Coordinates, &rings); err != nil {
				return nil, fmt.Errorf("country %s: %w", p.Code, err)
			}
			geometry = qwibi.Polygon(rings...)
			polygons = [][][][2]float64{rings}
		case "MultiPolygon":
			if err := json.Unmarshal(feature.Geometry.Coordinates, &polygons); err != nil {
				return nil, fmt.Errorf("country %s: %w", p.Code, err)
			}
			geometry = qwibi.MultiPolygon(polygons...)
		default:
			return nil, fmt.Errorf("country %s: unsupported geometry %q", p.Code, feature.Geometry.Type)
		}
		result = append(result, Country{
			Code:      p.Code,
			ISOA2:     p.ISOA2,
			Name:      p.Name,
			Continent: p.Continent,
			Subregion: p.Subregion,
			Geometry:  geometry,
			polygons:  polygons,
		})
	}
	return result, nil
}

// Objects returns the complete App data of one version: one object of type
// "country" per outline. Publishing replaces the App's whole data set with it.
//
// An object carries only properties its version declares. That is what keeps
// the next version compatible: Qwibi refuses to add a declared property whose
// key already sits in stored data (see compat.go).
func Objects(v Version) ([]*pb.ObjectWrite, error) {
	all, err := Countries()
	if err != nil {
		return nil, err
	}
	style, err := CountryStyle.MarshalStyle()
	if err != nil {
		return nil, err
	}
	objects := make([]*pb.ObjectWrite, 0, len(all))
	for _, country := range all {
		properties := map[string]any{
			"code":      country.Code,
			"name":      country.Name,
			"continent": country.Continent,
		}
		if country.ISOA2 != "" {
			properties["iso_a2"] = country.ISOA2
		}
		if v.Subregion && country.Subregion != "" {
			properties["subregion"] = country.Subregion
		}
		props, err := structpb.NewStruct(properties)
		if err != nil {
			return nil, fmt.Errorf("country %s properties: %w", country.Code, err)
		}
		objects = append(objects, &pb.ObjectWrite{
			Hid:        country.HID(),
			Name:       country.Name,
			ObjectType: ObjectType,
			Geometry:   country.Geometry,
			Properties: props,
			Style:      style,
		})
	}
	return objects, nil
}
