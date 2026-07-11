package structure

type Keyed[K comparable] interface {
	Key() K
}

type CompareFunc[T any] func(a, b T) int

type EqualFunc[T any] func(a, b T) bool

type Iterator[T any] interface {
	Next() bool
	Value() T
}
