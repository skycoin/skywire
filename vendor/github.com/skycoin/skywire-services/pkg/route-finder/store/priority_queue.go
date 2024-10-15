package store

import (
	"container/heap"
)

// implementation from https://rosettacode.org/wiki/Dijkstra%27s_algorithm#Go
// A priorityQueue implements heap.Interface and holds Items.
type priorityQueue struct {
	items []*vertex
	// value to index
	m map[*vertex]int
	// value to priority
	pr map[*vertex]int
}

func (pq *priorityQueue) Len() int           { return len(pq.items) }
func (pq *priorityQueue) Less(i, j int) bool { return pq.pr[pq.items[i]] < pq.pr[pq.items[j]] }
func (pq *priorityQueue) Swap(i, j int) {
	pq.items[i], pq.items[j] = pq.items[j], pq.items[i]
	pq.m[pq.items[i]] = i
	pq.m[pq.items[j]] = j
}

func (pq *priorityQueue) Push(x interface{}) {
	n := len(pq.items)
	item := x.(*vertex)
	pq.m[item] = n
	pq.items = append(pq.items, item)
}
func (pq *priorityQueue) Pop() interface{} {
	old := pq.items
	n := len(old)
	item := old[n-1]
	pq.m[item] = -1
	pq.items = old[0 : n-1]
	return item
}

// update modifies the priority of an item in the queue.
func (pq *priorityQueue) update(item *vertex, priority int) {
	pq.pr[item] = priority
	heap.Fix(pq, pq.m[item])
}
func (pq *priorityQueue) addWithPriority(item *vertex, priority int) {
	heap.Push(pq, item)
	pq.update(item, priority)
}
