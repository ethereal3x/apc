package structure

import (
	"reflect"
	"testing"
)

func TestLinkedListPushAndRange(t *testing.T) {
	list := NewLinkedList[int]()
	list.PushBack(2)
	list.PushFront(1)
	list.PushBack(3)

	if list.Len() != 3 {
		t.Fatalf("Len() = %d, want 3", list.Len())
	}
	if list.Empty() {
		t.Fatal("Empty() = true, want false")
	}
	if list.Front().Value != 1 {
		t.Fatalf("Front().Value = %d, want 1", list.Front().Value)
	}
	if list.Back().Value != 3 {
		t.Fatalf("Back().Value = %d, want 3", list.Back().Value)
	}

	var got []int
	list.Range(func(v int) bool {
		got = append(got, v)
		return true
	})
	want := []int{1, 2, 3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Range() = %v, want %v", got, want)
	}
}

func TestLinkedListPopAndRemove(t *testing.T) {
	list := NewLinkedList[string]()
	first := list.PushBack("first")
	second := list.PushBack("second")
	third := list.PushBack("third")

	if first.Next() != second {
		t.Fatal("first.Next() does not point to second")
	}
	if third.Prev() != second {
		t.Fatal("third.Prev() does not point to second")
	}

	removed := list.Remove(second)
	if removed != "second" {
		t.Fatalf("Remove() = %q, want second", removed)
	}
	if list.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", list.Len())
	}

	v, ok := list.PopFront()
	if !ok || v != "first" {
		t.Fatalf("PopFront() = %q, %v, want first, true", v, ok)
	}
	v, ok = list.PopBack()
	if !ok || v != "third" {
		t.Fatalf("PopBack() = %q, %v, want third, true", v, ok)
	}
	_, ok = list.PopFront()
	if ok {
		t.Fatal("PopFront() ok = true, want false")
	}
}

func TestLinkedListFindRangeStopAndClear(t *testing.T) {
	list := NewLinkedList[int]()
	for i := 1; i <= 5; i++ {
		list.PushBack(i)
	}

	node := list.Find(func(v int) bool {
		return v%2 == 0
	})
	if node == nil || node.Value != 2 {
		t.Fatalf("Find() = %v, want node with value 2", node)
	}

	var got []int
	list.Range(func(v int) bool {
		got = append(got, v)
		return v < 3
	})
	want := []int{1, 2, 3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Range() with stop = %v, want %v", got, want)
	}

	list.Clear()
	if !list.Empty() {
		t.Fatalf("Empty() = false after Clear")
	}
	if list.Front() != nil || list.Back() != nil {
		t.Fatal("Front()/Back() should be nil after Clear")
	}
}

type testUser struct {
	id   int64
	name string
}

func (u testUser) Key() int64 {
	return u.id
}

func TestKeyedCompareFuncAndEqualFunc(t *testing.T) {
	var keyed Keyed[int64] = testUser{id: 1001, name: "tom"}
	if keyed.Key() != 1001 {
		t.Fatalf("Key() = %d, want 1001", keyed.Key())
	}

	compare := CompareFunc[int](func(a, b int) int {
		return a - b
	})
	if compare(1, 2) >= 0 || compare(2, 1) <= 0 || compare(2, 2) != 0 {
		t.Fatal("CompareFunc returned unexpected result")
	}

	equal := EqualFunc[testUser](func(a, b testUser) bool {
		return a.Key() == b.Key()
	})
	if !equal(testUser{id: 1}, testUser{id: 1}) {
		t.Fatal("EqualFunc returned false, want true")
	}
}
