package countries

import (
	"strings"
	"testing"
	"time"

	qwibi "github.com/qwibi/qwibi-go-sdk"
	pb "github.com/qwibi/qwibi-proto-go/qwibi/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// pair returns the real 1.0.0 release and data, and a copy of it as the
// candidate next version one week later.
func pair(t *testing.T) (*pb.AppRelease, *pb.AppRelease, []*pb.ObjectWrite) {
	t.Helper()
	v := mustVersion(t, "1.0.0")
	prev, err := SealedRelease(testApp, v)
	if err != nil {
		t.Fatal(err)
	}
	data, err := Objects(v)
	if err != nil {
		t.Fatal(err)
	}
	next := proto.Clone(prev).(*pb.AppRelease)
	next.SemanticVersion = "1.0.1"
	next.ReleaseId = qwibi.ReleaseIDFor(testApp, "1.0.1")
	next.PublishedAt = timestamppb.New(v.PublishedAt.Add(7 * 24 * time.Hour))
	return prev, next, data
}

func schemaOf(r *pb.AppRelease) map[string]any { return r.GetObjectSchemas()[0].GetSchema().AsMap() }

func setSchema(t *testing.T, r *pb.AppRelease, schema map[string]any) {
	t.Helper()
	s, err := structpb.NewStruct(schema)
	if err != nil {
		t.Fatal(err)
	}
	r.ObjectSchemas[0].Schema = s
}

func property(schema map[string]any, name string) map[string]any {
	return schema["properties"].(map[string]any)[name].(map[string]any)
}

func TestTheDeclaredUpdateIsCompatible(t *testing.T) {
	prev, err := Release(testApp, mustVersion(t, "1.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	next, err := Release(testApp, mustVersion(t, "1.1.0"))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := Objects(mustVersion(t, "1.0.0"))
	if err := CheckCompatible(prev, next, data); err != nil {
		t.Fatal(err)
	}
	// Going back is not: 1.1.0 data carries subregion, which 1.0.0 does not
	// admit.
	newer, _ := Objects(mustVersion(t, "1.1.0"))
	back := proto.Clone(prev).(*pb.AppRelease)
	back.PublishedAt = timestamppb.New(next.GetPublishedAt().AsTime().Add(time.Hour))
	if err := CheckCompatible(next, back, newer); err == nil {
		t.Fatal("dropping subregion while stored objects carry it was accepted")
	}
}

func TestIncompatibleChangesAreRefused(t *testing.T) {
	cases := map[string]func(t *testing.T, next *pb.AppRelease, data []*pb.ObjectWrite) []*pb.ObjectWrite{
		"object type removed while it has data": func(t *testing.T, next *pb.AppRelease, data []*pb.ObjectWrite) []*pb.ObjectWrite {
			next.ObjectSchemas[0].ObjectType = "place"
			return data
		},
		"property becomes required": func(t *testing.T, next *pb.AppRelease, data []*pb.ObjectWrite) []*pb.ObjectWrite {
			s := schemaOf(next)
			s["required"] = append(s["required"].([]any), "iso_a2")
			setSchema(t, next, s)
			return data
		},
		"property type changes": func(t *testing.T, next *pb.AppRelease, data []*pb.ObjectWrite) []*pb.ObjectWrite {
			s := schemaOf(next)
			property(s, "name")["type"] = "integer"
			setSchema(t, next, s)
			return data
		},
		"a constraint tightens": func(t *testing.T, next *pb.AppRelease, data []*pb.ObjectWrite) []*pb.ObjectWrite {
			s := schemaOf(next)
			property(s, "name")["minLength"] = 3
			setSchema(t, next, s)
			return data
		},
		"declared property removed while stored": func(t *testing.T, next *pb.AppRelease, data []*pb.ObjectWrite) []*pb.ObjectWrite {
			s := schemaOf(next)
			delete(s["properties"].(map[string]any), "iso_a2")
			setSchema(t, next, s)
			return data
		},
		"added property already sits in stored data": func(t *testing.T, next *pb.AppRelease, data []*pb.ObjectWrite) []*pb.ObjectWrite {
			s := schemaOf(next)
			s["properties"].(map[string]any)["subregion"] = map[string]any{"type": "string"}
			setSchema(t, next, s)
			data[0].Properties.Fields["subregion"] = structpb.NewStringValue("Caribbean")
			return data
		},
		"published_at goes back": func(t *testing.T, next *pb.AppRelease, data []*pb.ObjectWrite) []*pb.ObjectWrite {
			next.PublishedAt = timestamppb.New(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
			return data
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			prev, next, data := pair(t)
			data = mutate(t, next, data)
			if err := CheckCompatible(prev, next, data); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestSafeChangesAreAccepted(t *testing.T) {
	cases := map[string]func(t *testing.T, next *pb.AppRelease){
		"nothing changes but the version": func(t *testing.T, next *pb.AppRelease) {},
		"a type widens": func(t *testing.T, next *pb.AppRelease) {
			s := schemaOf(next)
			property(s, "iso_a2")["type"] = []any{"string", "null"}
			setSchema(t, next, s)
		},
		"a property stops being required": func(t *testing.T, next *pb.AppRelease) {
			s := schemaOf(next)
			s["required"] = []any{"code", "name"}
			setSchema(t, next, s)
		},
		"a new property no object carries": func(t *testing.T, next *pb.AppRelease) {
			s := schemaOf(next)
			s["properties"].(map[string]any)["capital"] = map[string]any{"type": "string"}
			setSchema(t, next, s)
		},
		"a label changes": func(t *testing.T, next *pb.AppRelease) {
			next.Localizations[0].Entries["country.code"] = "Country code"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			prev, next, data := pair(t)
			mutate(t, next)
			if err := CheckCompatible(prev, next, data); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWithoutDataFewerRulesApply(t *testing.T) {
	prev, next, _ := pair(t)
	s := schemaOf(next)
	property(s, "name")["minLength"] = 3
	setSchema(t, next, s)
	if err := CheckCompatible(prev, next, nil); err != nil {
		t.Fatalf("with no stored objects a tighter constraint strands nothing: %v", err)
	}
	next.ObjectSchemas[0].ObjectType = "place"
	if err := CheckCompatible(prev, next, nil); err != nil {
		t.Fatalf("an unused type may go: %v", err)
	}
	for name, mutate := range map[string]func(map[string]any){
		"type changes":      func(s map[string]any) { property(s, "name")["type"] = "integer" },
		"even when widened": func(s map[string]any) { property(s, "name")["type"] = []any{"string", "null"} },
		"newly required":    func(s map[string]any) { s["required"] = append(s["required"].([]any), "iso_a2") },
	} {
		_, next, _ := pair(t)
		s := schemaOf(next)
		mutate(s)
		setSchema(t, next, s)
		if err := CheckCompatible(prev, next, nil); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Why 1.0.0 declares additionalProperties: false. Without it the former
// schema admits "subregion" with any value, and declaring it as a string
// narrows what stored objects may hold.
func TestOpenSchemaCannotGainAProperty(t *testing.T) {
	prev, next, data := pair(t)
	open := schemaOf(prev)
	delete(open, "additionalProperties")
	setSchema(t, prev, open)
	widened := schemaOf(prev)
	widened["properties"].(map[string]any)["subregion"] = map[string]any{"type": "string"}
	setSchema(t, next, widened)
	err := CheckCompatible(prev, next, data)
	if err == nil || !strings.Contains(err.Error(), "additionalProperties") {
		t.Fatalf("got %v, want a refusal that points at additionalProperties", err)
	}
}
