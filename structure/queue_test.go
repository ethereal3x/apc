package structure

import "testing"

func TestQueue(t *testing.T) {
	queue := NewQueue[string]()
	if !queue.Empty() {
		t.Fatal("new queue should be empty")
	}

	queue.Enqueue("first")
	queue.Enqueue("second")
	queue.Enqueue("third")

	if queue.Len() != 3 {
		t.Fatalf("Len() = %d, want 3", queue.Len())
	}
	v, ok := queue.Peek()
	if !ok || v != "first" {
		t.Fatalf("Peek() = %q, %v, want first, true", v, ok)
	}
	if queue.Len() != 3 {
		t.Fatalf("Len() after Peek = %d, want 3", queue.Len())
	}

	for _, want := range []string{"first", "second", "third"} {
		v, ok = queue.Dequeue()
		if !ok || v != want {
			t.Fatalf("Dequeue() = %q, %v, want %q, true", v, ok, want)
		}
	}
	_, ok = queue.Dequeue()
	if ok {
		t.Fatal("Dequeue() ok = true, want false")
	}

	queue.Enqueue("again")
	queue.Clear()
	if !queue.Empty() {
		t.Fatal("queue should be empty after Clear")
	}
}

func TestQueueShrinkAndReuse(t *testing.T) {
	queue := NewQueue[int]()
	for i := 0; i < 130; i++ {
		queue.Enqueue(i)
	}
	for i := 0; i < 100; i++ {
		v, ok := queue.Dequeue()
		if !ok || v != i {
			t.Fatalf("Dequeue() = %d, %v, want %d, true", v, ok, i)
		}
	}
	if queue.Len() != 30 {
		t.Fatalf("Len() = %d, want 30", queue.Len())
	}
	queue.Enqueue(130)

	v, ok := queue.Peek()
	if !ok || v != 100 {
		t.Fatalf("Peek() = %d, %v, want 100, true", v, ok)
	}
}
