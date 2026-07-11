package structure

type Stack[T any] struct {
	values []T
}

func NewStack[T any]() *Stack[T] {
	return &Stack[T]{}
}

func (s *Stack[T]) Len() int {
	if s == nil {
		return 0
	}
	return len(s.values)
}

func (s *Stack[T]) Empty() bool {
	return s.Len() == 0
}

func (s *Stack[T]) Push(v T) {
	s.values = append(s.values, v)
}

func (s *Stack[T]) Pop() (T, bool) {
	if s == nil || len(s.values) == 0 {
		var zero T
		return zero, false
	}
	idx := len(s.values) - 1
	v := s.values[idx]
	var zero T
	s.values[idx] = zero
	s.values = s.values[:idx]
	return v, true
}

func (s *Stack[T]) Peek() (T, bool) {
	if s == nil || len(s.values) == 0 {
		var zero T
		return zero, false
	}
	return s.values[len(s.values)-1], true
}

func (s *Stack[T]) Clear() {
	if s == nil {
		return
	}
	var zero T
	for i := range s.values {
		s.values[i] = zero
	}
	s.values = nil
}
