package app

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
)

// encoding/json accepts case-insensitive struct aliases. The wire protocol
// does not: a preview and its apply must see exactly the same field names.
// Raw user metadata remains opaque; it is never decoded into protocol fields.
func portableCanonicalFields(raw []byte, typ reflect.Type) error {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ == reflect.TypeOf(json.RawMessage{}) {
		return nil
	}
	switch typ.Kind() {
	case reflect.Struct:
		fields := map[string]reflect.Type{}
		var collect func(reflect.Type)
		collect = func(t reflect.Type) {
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				if f.PkgPath != "" {
					continue
				}
				tag := strings.Split(f.Tag.Get("json"), ",")[0]
				if tag == "-" {
					continue
				}
				if f.Anonymous && tag == "" {
					collect(f.Type)
					continue
				}
				if tag == "" {
					tag = f.Name
				}
				fields[tag] = f.Type
			}
		}
		collect(typ)
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			return err
		}
		for key, value := range object {
			field, ok := fields[key]
			if !ok {
				return errors.New("bundle fields must use canonical protocol names")
			}
			if err := portableCanonicalFields(value, field); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		if typ.Elem().Kind() == reflect.Uint8 {
			return nil
		}
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
		for _, value := range values {
			if err := portableCanonicalFields(value, typ.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}
