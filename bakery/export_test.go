package bakery

func MemOpsStoreLen(store OpsStore) int {
	return len(store.(*memOpsStore).ops)
}
