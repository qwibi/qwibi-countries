package countries

import (
	"errors"
	"fmt"
	"reflect"
	"sort"

	pb "github.com/qwibi/qwibi-proto-go/qwibi/v1"
)

// CheckCompatible tells, before anything is sent, whether Qwibi will accept
// next as the successor of prev when prevData is what the App has stored.
//
// Qwibi publishes a new version to every installation at once, so it refuses
// a version that would strand stored objects or the people who marked them:
//
//   - the new version is later: published_at advances (or, when equal, the
//     release id does);
//   - an object type that has stored objects stays declared;
//   - no property becomes required;
//   - without stored objects, every property kept keeps exactly its "type";
//   - with stored objects, every schema keyword stays the same except safe
//     widenings: "type" may admit more, "enum" may list more, "required" may
//     list fewer, and a declared property may be added only where the former
//     schema did not admit that key and no stored object carries it.
//
// This mirrors the rule the Qwibi API applies at publication for the keywords
// this App uses. Any other keyword must stay unchanged, which errs on the side
// of refusing. The server remains the authority.
func CheckCompatible(prev, next *pb.AppRelease, prevData []*pb.ObjectWrite) error {
	var problems []error
	prevAt, nextAt := prev.GetPublishedAt().AsTime(), next.GetPublishedAt().AsTime()
	if nextAt.Before(prevAt) || (nextAt.Equal(prevAt) && next.GetReleaseId() <= prev.GetReleaseId()) {
		problems = append(problems, errors.New("published_at and release_id must advance the current release"))
	}
	stored := map[string][]*pb.ObjectWrite{}
	for _, object := range prevData {
		stored[object.GetObjectType()] = append(stored[object.GetObjectType()], object)
	}
	nextSchemas := map[string]map[string]any{}
	for _, s := range next.GetObjectSchemas() {
		nextSchemas[s.GetObjectType()] = s.GetSchema().AsMap()
	}
	for _, s := range prev.GetObjectSchemas() {
		objectType := s.GetObjectType()
		objects := stored[objectType]
		nextSchema, declared := nextSchemas[objectType]
		if !declared {
			if len(objects) > 0 {
				problems = append(problems, fmt.Errorf("object type %q has stored objects and must stay declared", objectType))
			}
			continue
		}
		prevSchema := s.GetSchema().AsMap()
		if len(objects) == 0 {
			if err := compareUnused(objectType, prevSchema, nextSchema); err != nil {
				problems = append(problems, err)
			}
			continue
		}
		keyInUse := func(key string) bool {
			for _, object := range objects {
				if _, ok := object.GetProperties().GetFields()[key]; ok {
					return true
				}
			}
			return false
		}
		if err := compareStored(objectType, prevSchema, nextSchema, true, keyInUse); err != nil {
			problems = append(problems, err)
		}
	}
	return errors.Join(problems...)
}

// compareUnused compares the schemas of a type no stored object uses yet:
// every property kept keeps exactly its "type", and nothing becomes required.
func compareUnused(path string, prev, next map[string]any) error {
	if !reflect.DeepEqual(prev["type"], next["type"]) {
		return fmt.Errorf("%s: type changes", path)
	}
	prevProps, _ := prev["properties"].(map[string]any)
	nextProps, _ := next["properties"].(map[string]any)
	for _, name := range sortedKeys(prevProps) {
		prevProp, _ := prevProps[name].(map[string]any)
		nextProp, present := nextProps[name].(map[string]any)
		if !present {
			continue
		}
		if err := compareUnused(path+"."+name, prevProp, nextProp); err != nil {
			return err
		}
	}
	prevItems, prevOK := prev["items"].(map[string]any)
	nextItems, nextOK := next["items"].(map[string]any)
	if prevOK && nextOK {
		if err := compareUnused(path+".items", prevItems, nextItems); err != nil {
			return err
		}
	}
	was := map[any]bool{}
	for _, name := range asList(prev["required"]) {
		was[name] = true
	}
	for _, name := range asList(next["required"]) {
		if !was[name] {
			return fmt.Errorf("%s: property %v becomes required", path, name)
		}
	}
	return nil
}

// compareStored compares two schemas that stored data was written under.
// root is true for the object's own schema, whose properties are the stored
// keys that keyInUse can look up.
func compareStored(path string, prev, next map[string]any, root bool, keyInUse func(string) bool) error {
	keys := map[string]bool{}
	for k := range prev {
		keys[k] = true
	}
	for k := range next {
		keys[k] = true
	}
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, key := range names {
		prevValue, inPrev := prev[key]
		nextValue, inNext := next[key]
		at := path + "." + key
		switch key {
		case "type":
			if !inPrev || !inNext || !superset(typeSet(nextValue), typeSet(prevValue)) {
				return fmt.Errorf("%s: may only admit more types", at)
			}
		case "enum":
			if !inPrev || !inNext || !superset(asSet(nextValue), asSet(prevValue)) {
				return fmt.Errorf("%s: may only list more values", at)
			}
		case "required":
			if !superset(asSet(prevValue), asSet(nextValue)) {
				return fmt.Errorf("%s: may only list fewer properties", at)
			}
		case "properties":
			prevProps, _ := prevValue.(map[string]any)
			nextProps, _ := nextValue.(map[string]any)
			for _, name := range sortedKeys(prevProps) {
				prevProp, _ := prevProps[name].(map[string]any)
				nextProp, present := nextProps[name].(map[string]any)
				if present {
					if err := compareStored(at+"."+name, prevProp, nextProp, false, nil); err != nil {
						return err
					}
					continue
				}
				// A removed declaration falls back to additionalProperties.
				fallback, admitted := admits(next)
				if !admitted {
					if !root || keyInUse(name) {
						return fmt.Errorf("%s.%s: stored objects carry it, so it must stay declared", at, name)
					}
					continue
				}
				if err := compareStored(at+"."+name, prevProp, fallback, false, nil); err != nil {
					return err
				}
			}
			for _, name := range sortedKeys(nextProps) {
				if _, existed := prevProps[name]; existed {
					continue
				}
				fallback, admitted := admits(prev)
				if !admitted {
					if !root || keyInUse(name) {
						return fmt.Errorf("%s.%s: stored objects already carry this key", at, name)
					}
					continue
				}
				nextProp, _ := nextProps[name].(map[string]any)
				if err := compareStored(at+"."+name, fallback, nextProp, false, nil); err != nil {
					return fmt.Errorf("%s.%s: the former schema admitted this key with any value, so declaring it narrows stored data (declare additionalProperties: false from the first version): %w", at, name, err)
				}
			}
		default:
			if inPrev != inNext || !reflect.DeepEqual(prevValue, nextValue) {
				return fmt.Errorf("%s: must stay unchanged while objects are stored", at)
			}
		}
	}
	return nil
}

// admits returns the schema an undeclared key falls under, and whether such a
// key is admitted at all.
func admits(schema map[string]any) (map[string]any, bool) {
	switch additional := schema["additionalProperties"].(type) {
	case nil:
		return map[string]any{}, true
	case bool:
		return map[string]any{}, additional
	case map[string]any:
		return additional, true
	default:
		return nil, false
	}
}

func typeSet(value any) map[any]bool {
	if s, ok := value.(string); ok {
		return map[any]bool{s: true}
	}
	return asSet(value)
}

func asList(value any) []any {
	list, _ := value.([]any)
	return list
}

func asSet(value any) map[any]bool {
	set := map[any]bool{}
	for _, v := range asList(value) {
		set[v] = true
	}
	return set
}

func superset(big, small map[any]bool) bool {
	for v := range small {
		if !big[v] {
			return false
		}
	}
	return true
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
