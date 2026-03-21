package registry

import (
	"reflect"
)

// A Types represents mapping from registered names
// of the Registry to reflect.Type and inversed way
type Types struct {
	Direct  map[string]reflect.Type // registered name -> refelect.Type
	Inverse map[reflect.Type]string // refelct.Type -> registered name
}

// SchemaName returns schema name of given object
func (t *Types) SchemaName(obj interface{}) (name string, err error) {
	var ok bool
	if name, ok = t.Inverse[typeOf(obj)]; ok {
		return
	}
	err = ErrTypeNotFound
	return
}
