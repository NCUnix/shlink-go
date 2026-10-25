package types

import (
	"bytes"
	"encoding/json"
)

type OmittableNullable[T any] struct {
	Set   bool
	Valid bool
	Value T
}

func (o *OmittableNullable[T]) UnmarshalJSON(data []byte) error {
	o.Set = true
	if bytes.Equal(data, []byte("null")) {
		o.Valid = false
		var zero T
		o.Value = zero
		return nil
	}
	o.Valid = true
	return json.Unmarshal(data, &o.Value)
}

func (o OmittableNullable[T]) MarshalJSON() ([]byte, error) {
	if !o.Set || !o.Valid {
		return []byte("null"), nil
	}
	return json.Marshal(o.Value)
}

func (o OmittableNullable[T]) ValuePtr() *T {
	if !o.Set || !o.Valid {
		return nil
	}
	value := o.Value
	return &value
}
