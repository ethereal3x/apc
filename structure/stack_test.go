package structure

import "testing"

func TestStack(t *testing.T) {
	stack := NewStack[int]()
	if !stack.Empty() {
		t.Fatal("new stack should be empty")
	}

	stack.Push(1)
	stack.Push(2)
	stack.Push(3)

	if stack.Len() != 3 {
		t.Fatalf("Len() = %d, want 3", stack.Len())
	}
	v, ok := stack.Peek()
	if !ok || v != 3 {
		t.Fatalf("Peek() = %d, %v, want 3, true", v, ok)
	}
	if stack.Len() != 3 {
		t.Fatalf("Len() after Peek = %d, want 3", stack.Len())
	}

	for _, want := range []int{3, 2, 1} {
		v, ok = stack.Pop()
		if !ok || v != want {
			t.Fatalf("Pop() = %d, %v, want %d, true", v, ok, want)
		}
	}
	_, ok = stack.Pop()
	if ok {
		t.Fatal("Pop() ok = true, want false")
	}

	stack.Push(1)
	stack.Clear()
	if !stack.Empty() {
		t.Fatal("stack should be empty after Clear")
	}
}
