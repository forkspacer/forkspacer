package utils

import "maps"

func ToPtr[T any](value T) *T {
	return &value
}

func InitMap[K comparable, V any, T ~map[K]V](m *T) {
	if *m == nil {
		*m = make(T)
	}
}

func UpdateMap[K comparable, V any, T ~map[K]V](old *T, new T) {
	InitMap(old)
	maps.Copy(*old, new)
}
