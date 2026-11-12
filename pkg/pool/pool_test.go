package pool

import (
	"reflect"
	"testing"
)

func TestKeyPool(t *testing.T) {
	pool := New("key1, key2,  key3  ")
	if pool.Size() != 3 {
		t.Fatalf("expected size 3, got %d", pool.Size())
	}

	// 1st call starts with key1
	first := pool.OrderedKeys()
	expectedFirst := []string{"key1", "key2", "key3"}
	if !reflect.DeepEqual(first, expectedFirst) {
		t.Errorf("expected %v, got %v", expectedFirst, first)
	}

	// 2nd call starts with key2
	second := pool.OrderedKeys()
	expectedSecond := []string{"key2", "key3", "key1"}
	if !reflect.DeepEqual(second, expectedSecond) {
		t.Errorf("expected %v, got %v", expectedSecond, second)
	}

	// 3rd call starts with key3
	third := pool.OrderedKeys()
	expectedThird := []string{"key3", "key1", "key2"}
	if !reflect.DeepEqual(third, expectedThird) {
		t.Errorf("expected %v, got %v", expectedThird, third)
	}

	// 4th call wraps around to key1
	fourth := pool.OrderedKeys()
	if !reflect.DeepEqual(fourth, expectedFirst) {
		t.Errorf("expected %v, got %v", expectedFirst, fourth)
	}
}

func TestEmptyPool(t *testing.T) {
	empty := New("")
	if !empty.IsEmpty() {
		t.Errorf("expected empty pool")
	}
	if keys := empty.OrderedKeys(); keys != nil {
		t.Errorf("expected nil keys, got %v", keys)
	}
}
