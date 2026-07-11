package structure

type Queue[T any] struct {
	values []T
	head   int
}

func NewQueue[T any]() *Queue[T] {
	return &Queue[T]{}
}

func (q *Queue[T]) Len() int {
	if q == nil {
		return 0
	}
	return len(q.values) - q.head
}

func (q *Queue[T]) Empty() bool {
	return q.Len() == 0
}

func (q *Queue[T]) Enqueue(v T) {
	q.values = append(q.values, v)
}

func (q *Queue[T]) Dequeue() (T, bool) {
	if q == nil || q.Empty() {
		var zero T
		return zero, false
	}
	v := q.values[q.head]
	var zero T
	q.values[q.head] = zero
	q.head++
	q.shrink()
	return v, true
}

func (q *Queue[T]) Peek() (T, bool) {
	if q == nil || q.Empty() {
		var zero T
		return zero, false
	}
	return q.values[q.head], true
}

func (q *Queue[T]) Clear() {
	if q == nil {
		return
	}
	var zero T
	for i := range q.values {
		q.values[i] = zero
	}
	q.values = nil
	q.head = 0
}

func (q *Queue[T]) shrink() {
	if q.head == len(q.values) {
		q.values = nil
		q.head = 0
		return
	}
	if q.head > 64 && q.head*2 >= len(q.values) {
		q.values = append([]T(nil), q.values[q.head:]...)
		q.head = 0
	}
}
