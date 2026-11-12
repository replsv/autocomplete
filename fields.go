package autocomplete

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
)

const tagName = "searchindex"

var (
	// ErrNoFields is returned by New when the item type has no field tagged searchindex:"true", or is not a
	// struct or a pointer to one.
	ErrNoFields = errors.New("autocomplete: no field tagged searchindex:\"true\"")
	// ErrNoID is returned by New when neither a searchindex:"id" field nor WithID gives the item id.
	ErrNoID = errors.New("autocomplete: no id: tag a string field searchindex:\"id\" or use WithID")
	// ErrInvalidTag is wrapped by the errors New returns for a malformed or misplaced searchindex tag.
	ErrInvalidTag = errors.New("autocomplete: invalid searchindex tag")
)

type fieldSpec struct {
	name  string
	index []int
	ptr   bool // *string
	boost float64
}

// get reads the field from the struct v. An invalid v (a nil item), a nil embedded struct pointer or a nil
// *string reads as "".
func (f *fieldSpec) get(v reflect.Value) string {
	if !v.IsValid() {
		return ""
	}
	fv, err := v.FieldByIndexErr(f.index)
	if err != nil {
		return ""
	}
	if f.ptr {
		if fv.IsNil() {
			return ""
		}
		fv = fv.Elem()
	}
	return fv.String()
}

type layout struct {
	fields []fieldSpec
	id     *fieldSpec
	ptr    bool // the item type is a pointer to the struct
}

func parseLayout(t reflect.Type) (layout, error) {
	var l layout
	if t.Kind() == reflect.Pointer {
		l.ptr = true
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return l, fmt.Errorf("%w: %v is not a struct or a pointer to one", ErrNoFields, t)
	}

	for _, sf := range reflect.VisibleFields(t) {
		tag, ok := sf.Tag.Lookup(tagName)
		if !ok {
			continue
		}
		role, opts, _ := strings.Cut(tag, ",")
		if role == "-" || role == "false" {
			continue
		}
		switch role {
		case "id":
			switch {
			case opts != "":
				return l, fmt.Errorf("%w: field %s: id takes no options", ErrInvalidTag, sf.Name)
			case sf.Type.Kind() != reflect.String:
				return l, fmt.Errorf("%w: field %s: id must be a string, not %v", ErrInvalidTag, sf.Name, sf.Type)
			case l.id != nil:
				return l, fmt.Errorf("%w: field %s: id already tagged on %s", ErrInvalidTag, sf.Name, l.id.name)
			}
			l.id = &fieldSpec{name: sf.Name, index: sf.Index}
		case "true":
			spec := fieldSpec{name: sf.Name, index: sf.Index}
			switch {
			case sf.Type.Kind() == reflect.String:
			case sf.Type.Kind() == reflect.Pointer && sf.Type.Elem().Kind() == reflect.String:
				spec.ptr = true
			default:
				return l, fmt.Errorf("%w: field %s: want string or *string, not %v", ErrInvalidTag, sf.Name, sf.Type)
			}
			if err := spec.parseOptions(opts); err != nil {
				return l, fmt.Errorf("%w: field %s: %v", ErrInvalidTag, sf.Name, err)
			}
			l.fields = append(l.fields, spec)
		default:
			return l, fmt.Errorf("%w: field %s: unknown value %q", ErrInvalidTag, sf.Name, role)
		}
	}

	if len(l.fields) == 0 {
		return l, ErrNoFields
	}
	return l, nil
}

func (f *fieldSpec) parseOptions(opts string) error {
	if opts == "" {
		return nil
	}
	for o := range strings.SplitSeq(opts, ",") {
		key, val, _ := strings.Cut(strings.TrimSpace(o), "=")
		switch key {
		case "boost":
			b, err := strconv.ParseFloat(val, 64)
			if err != nil || math.IsInf(b, 0) || math.IsNaN(b) {
				return fmt.Errorf("boost %q is not a finite number", val)
			}
			f.boost = b
		default:
			return fmt.Errorf("unknown option %q", o)
		}
	}
	return nil
}
