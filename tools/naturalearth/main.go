// Command naturalearth turns the public-domain Natural Earth 1:110m admin-0
// countries file into the compact data/countries.geojson this App embeds.
//
//	curl -fsSLo /tmp/ne.geojson \
//	  https://raw.githubusercontent.com/nvkelso/natural-earth-vector/master/geojson/ne_110m_admin_0_countries.geojson
//	go run ./tools/naturalearth /tmp/ne.geojson > data/countries.geojson
//
// It keeps five properties per country, rounds coordinates to four decimal
// places (about 11 m, far below the 1:110m resolution), drops consecutive
// duplicate positions produced by the rounding, and sorts countries by code so
// the output is byte-for-byte reproducible.
package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
)

type sourceFeature struct {
	Properties map[string]any `json:"properties"`
	Geometry   struct {
		Type        string          `json:"type"`
		Coordinates json.RawMessage `json:"coordinates"`
	} `json:"geometry"`
}

type outFeature struct {
	Type       string        `json:"type"`
	Properties outProperties `json:"properties"`
	Geometry   outGeometry   `json:"geometry"`
}

type outProperties struct {
	Code      string `json:"code"`
	ISOA2     string `json:"iso_a2,omitempty"`
	Name      string `json:"name"`
	Continent string `json:"continent"`
	Subregion string `json:"subregion"`
}

type outGeometry struct {
	Type        string `json:"type"`
	Coordinates any    `json:"coordinates"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: naturalearth <ne_110m_admin_0_countries.geojson>")
		os.Exit(2)
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, "naturalearth:", err)
		os.Exit(1)
	}
}

func run(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var source struct {
		Features []sourceFeature `json:"features"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		return err
	}
	out := make([]outFeature, 0, len(source.Features))
	seen := map[string]bool{}
	for _, feature := range source.Features {
		props := outProperties{
			Code:      text(feature.Properties, "ADM0_A3"),
			ISOA2:     text(feature.Properties, "ISO_A2_EH"),
			Name:      text(feature.Properties, "NAME"),
			Continent: text(feature.Properties, "CONTINENT"),
			Subregion: text(feature.Properties, "SUBREGION"),
		}
		if props.ISOA2 == "-99" {
			props.ISOA2 = ""
		}
		if props.Code == "" || props.Code == "-99" || seen[props.Code] {
			return fmt.Errorf("feature %q has a missing or repeated ADM0_A3 %q", props.Name, props.Code)
		}
		seen[props.Code] = true
		geometry, err := roundGeometry(feature.Geometry.Type, feature.Geometry.Coordinates)
		if err != nil {
			return fmt.Errorf("%s: %w", props.Code, err)
		}
		out = append(out, outFeature{Type: "Feature", Properties: props, Geometry: geometry})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Properties.Code < out[j].Properties.Code })

	// One feature per line keeps diffs of a future data refresh readable.
	var b strings.Builder
	b.WriteString("{\"type\":\"FeatureCollection\",\"features\":[\n")
	for i, feature := range out {
		line, err := json.Marshal(feature)
		if err != nil {
			return err
		}
		b.Write(line)
		if i < len(out)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("]}\n")
	_, err = os.Stdout.WriteString(b.String())
	return err
}

func text(props map[string]any, key string) string {
	value, _ := props[key].(string)
	return strings.TrimSpace(value)
}

func roundGeometry(kind string, raw json.RawMessage) (outGeometry, error) {
	switch kind {
	case "Polygon":
		var rings [][][2]float64
		if err := json.Unmarshal(raw, &rings); err != nil {
			return outGeometry{}, err
		}
		polygon := roundPolygon(rings)
		if polygon == nil {
			return outGeometry{}, fmt.Errorf("polygon collapses when rounded")
		}
		return outGeometry{Type: kind, Coordinates: polygon}, nil
	case "MultiPolygon":
		var polygons [][][][2]float64
		if err := json.Unmarshal(raw, &polygons); err != nil {
			return outGeometry{}, err
		}
		kept := polygons[:0]
		for _, polygon := range polygons {
			// Natural Earth carries a few slivers micrometres across; they
			// collapse when rounded and are dropped.
			if rounded := roundPolygon(polygon); rounded != nil {
				kept = append(kept, rounded)
			}
		}
		switch len(kept) {
		case 0:
			return outGeometry{}, fmt.Errorf("every polygon collapses when rounded")
		case 1:
			return outGeometry{Type: "Polygon", Coordinates: kept[0]}, nil
		}
		return outGeometry{Type: kind, Coordinates: kept}, nil
	default:
		return outGeometry{}, fmt.Errorf("unsupported geometry %q", kind)
	}
}

// roundPolygon returns nil when the exterior ring collapses; a collapsed hole
// is dropped.
func roundPolygon(rings [][][2]float64) [][][2]float64 {
	kept := rings[:0]
	for i, ring := range rings {
		rounded := make([][2]float64, 0, len(ring))
		for _, p := range ring {
			q := [2]float64{round4(p[0]), round4(p[1])}
			if len(rounded) > 0 && rounded[len(rounded)-1] == q {
				continue
			}
			rounded = append(rounded, q)
		}
		if len(rounded) < 4 {
			if i == 0 {
				return nil
			}
			continue
		}
		kept = append(kept, rounded)
	}
	return kept
}

func round4(v float64) float64 { return math.Round(v*1e4) / 1e4 }
