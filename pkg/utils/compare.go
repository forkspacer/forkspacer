package utils

func NotNilAndNot[T comparable](value *T, compareTo T) bool {
	if value != nil && *value != compareTo {
		return true
	}

	return false
}

func NotNilAndZero[T comparable](value *T) bool {
	var zero T
	return NotNilAndNot(value, zero)
}
