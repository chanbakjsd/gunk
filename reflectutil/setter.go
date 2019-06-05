package reflectutil

import (
	"fmt"
	"go/ast"
	"reflect"
	"strconv"
	"strings"

	protop "github.com/emicklei/proto"
	"github.com/golang/protobuf/proto"
)

// SetValue assign value to the field name of the struct structPtr.
// Returns an error if name is not found or structPtr cannot
// be addressed.
func SetValue(structPtr interface{}, name, value interface{}) {
	nameStr := ""
	switch name := name.(type) {
	case *ast.Ident:
		nameStr = name.Name
	case string:
		nameStr = name
	default:
		panic(fmt.Errorf("invalid name type: %T", name))
	}

	// Performs a case insensitive search because generated structs by
	// protoc don't follow the best practices from the go naming convention:
	// initialisms should be all capitals.
	// Remove underscores too, so that foo_bar matches the field FooBar.
	nameStr = strings.ReplaceAll(nameStr, "_", "")
	field, ok := reflect.TypeOf(structPtr).Elem().FieldByNameFunc(func(s string) bool {
		return strings.EqualFold(s, nameStr)
	})
	if !ok {
		panic(fmt.Errorf("%s was not found in %T", nameStr, structPtr))
	}
	fval := reflect.ValueOf(structPtr).Elem().FieldByIndex(field.Index)

	val := valueFor(field.Type, field.Tag, value)
	switch field.Type.Kind() {
	case reflect.Slice:
		val = reflect.AppendSlice(fval, val)
	case reflect.Map:
		if fval.IsNil() {
			fval.Set(reflect.MakeMap(field.Type))
		}
		iter := val.MapRange()
		for iter.Next() {
			fval.SetMapIndex(iter.Key(), iter.Value())
		}
		return
	}
	fval.Set(val)
}

func valueFor(typ reflect.Type, tag reflect.StructTag, value interface{}) reflect.Value {
	if named, ok := value.(*protop.NamedLiteral); ok {
		// We don't care about the name here.
		value = named.Literal
	}

	switch typ.Kind() {
	case reflect.Ptr:
		return valueFor(typ.Elem(), tag, value).Addr()
	case reflect.Struct:
		strc := reflect.New(typ)
		switch value := value.(type) {
		case *ast.CompositeLit:
			for _, elt := range value.Elts {
				kv := elt.(*ast.KeyValueExpr)
				SetValue(strc.Interface(), kv.Key, kv.Value)
			}
		case *protop.Literal:
			for _, lit := range value.OrderedMap {
				SetValue(strc.Interface(), lit.Name, lit)
			}
		default:
			panic(fmt.Sprintf("%T is not a valid value for %s", value, typ))
		}
		return strc.Elem()
	case reflect.Map:
		mp := reflect.MakeMap(typ)
		switch value := value.(type) {
		case *ast.CompositeLit:
			for _, elt := range value.Elts {
				kv := elt.(*ast.KeyValueExpr)
				key := valueFor(typ.Key(), "", kv.Key)
				val := valueFor(typ.Elem(), "", kv.Value)
				mp.SetMapIndex(key, val)
			}
		case *protop.Literal:
			key := valueFor(typ.Key(), "", value.OrderedMap[0])
			val := valueFor(typ.Elem(), "", value.OrderedMap[1])
			if key.Interface() == "empty" {
				// TODO(mvdan): figure out why this happens
				break
			}
			mp.SetMapIndex(key, val)
		default:
			panic(fmt.Sprintf("%T is not a valid value for %s", value, typ))
		}
		return mp
	case reflect.Slice:
		list := reflect.MakeSlice(typ, 0, 0)
		switch value := value.(type) {
		case *ast.CompositeLit:
			for _, elt := range value.Elts {
				list = reflect.Append(list, valueFor(typ.Elem(), tag, elt))
			}
		case *protop.Literal:
			if value.Array == nil {
				list = reflect.Append(list, valueFor(typ.Elem(), tag, value))
				break
			}
			for _, lit := range value.Array {
				list = reflect.Append(list, valueFor(typ.Elem(), tag, lit))
			}
		default:
			panic(fmt.Sprintf("%T is not a valid value for %s", value, typ))
		}
		return list
	}

	valueStr := ""
	switch x := value.(type) {
	case *ast.Ident:
		valueStr = x.Name
	case *ast.BasicLit:
		valueStr = x.Value
	case *ast.SelectorExpr:
		valueStr = x.Sel.Name
	case *protop.Literal:
		valueStr = x.SourceRepresentation()
	}
	value = reflect.Value{} // ensure we just use valueStr from this point

	// If the field is an enum, decode it, and store it via a conversion
	// from int32 to the named enum type.
	for _, kv := range strings.Split(tag.Get("protobuf"), ",") {
		kv := strings.SplitN(kv, "=", 2)
		if kv[0] != "enum" {
			continue
		}
		enumMap := proto.EnumValueMap(kv[1])
		val, ok := enumMap[valueStr]
		if !ok {
			panic(fmt.Errorf("%q is not a valid %s", valueStr, kv[1]))
		}
		return reflect.ValueOf(val).Convert(typ)
	}

	var v interface{}
	var err error
	switch typ.Kind() {
	case reflect.String:
		v, err = strconv.Unquote(valueStr)
	case reflect.Float64:
		v, err = strconv.ParseFloat(valueStr, 64)
	case reflect.Bool:
		v, err = strconv.ParseBool(valueStr)
	case reflect.Uint64:
		v, err = strconv.ParseUint(valueStr, 10, 64)
	}
	if err != nil {
		panic(err)
	}
	if v == nil {
		panic(fmt.Sprintf("%T is not a valid value for %s", valueStr, typ))
	}
	return reflect.ValueOf(v)
}
