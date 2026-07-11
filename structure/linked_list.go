package structure

type LinkedList[T any] struct {
	head *ListNode[T]
	tail *ListNode[T]
	len  int
}

type ListNode[T any] struct {
	Value T

	prev *ListNode[T]
	next *ListNode[T]
}

func NewLinkedList[T any]() *LinkedList[T] {
	return &LinkedList[T]{}
}

func (l *LinkedList[T]) Len() int {
	if l == nil {
		return 0
	}
	return l.len
}

func (l *LinkedList[T]) Empty() bool {
	return l.Len() == 0
}

func (l *LinkedList[T]) Front() *ListNode[T] {
	if l == nil {
		return nil
	}
	return l.head
}

func (l *LinkedList[T]) Back() *ListNode[T] {
	if l == nil {
		return nil
	}
	return l.tail
}

func (n *ListNode[T]) Next() *ListNode[T] {
	if n == nil {
		return nil
	}
	return n.next
}

func (n *ListNode[T]) Prev() *ListNode[T] {
	if n == nil {
		return nil
	}
	return n.prev
}

func (l *LinkedList[T]) PushFront(v T) *ListNode[T] {
	n := &ListNode[T]{Value: v}
	if l.head == nil {
		l.head = n
		l.tail = n
	} else {
		n.next = l.head
		l.head.prev = n
		l.head = n
	}
	l.len++
	return n
}

func (l *LinkedList[T]) PushBack(v T) *ListNode[T] {
	n := &ListNode[T]{Value: v}
	if l.tail == nil {
		l.head = n
		l.tail = n
	} else {
		n.prev = l.tail
		l.tail.next = n
		l.tail = n
	}
	l.len++
	return n
}

func (l *LinkedList[T]) PopFront() (T, bool) {
	if l == nil || l.head == nil {
		var zero T
		return zero, false
	}
	return l.Remove(l.head), true
}

func (l *LinkedList[T]) PopBack() (T, bool) {
	if l == nil || l.tail == nil {
		var zero T
		return zero, false
	}
	return l.Remove(l.tail), true
}

func (l *LinkedList[T]) Remove(n *ListNode[T]) T {
	if n.prev != nil {
		n.prev.next = n.next
	} else {
		l.head = n.next
	}

	if n.next != nil {
		n.next.prev = n.prev
	} else {
		l.tail = n.prev
	}

	n.prev = nil
	n.next = nil
	l.len--
	return n.Value
}

func (l *LinkedList[T]) Find(match func(T) bool) *ListNode[T] {
	if l == nil || match == nil {
		return nil
	}
	for n := l.Front(); n != nil; n = n.Next() {
		if match(n.Value) {
			return n
		}
	}
	return nil
}

func (l *LinkedList[T]) Range(fn func(T) bool) {
	if l == nil || fn == nil {
		return
	}
	for n := l.Front(); n != nil; n = n.Next() {
		if !fn(n.Value) {
			return
		}
	}
}

func (l *LinkedList[T]) Clear() {
	if l == nil {
		return
	}
	for n := l.head; n != nil; {
		next := n.next
		n.prev = nil
		n.next = nil
		n = next
	}
	l.head = nil
	l.tail = nil
	l.len = 0
}
