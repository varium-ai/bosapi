package interfaces

import "slices"

type gatewayDataStore interface {
	Load(any) error
	Save(any) error
}

// localDataSnapshot requires persistenceMu. All local data writers take that
// lock before mu, so the snapshot's maps remain stable until the save finishes.
// Readers only need mu and never wait for encryption or filesystem operations.
func (a *app) localDataSnapshot() data {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.data
}

func (a *app) load() error {
	a.persistenceMu.Lock()
	defer a.persistenceMu.Unlock()
	var next data
	if err := a.encryptedStore().Load(&next); err != nil {
		return err
	}
	a.mu.Lock()
	a.data = next
	a.mu.Unlock()
	return nil
}

func (a *app) save() error {
	a.persistenceMu.Lock()
	defer a.persistenceMu.Unlock()
	return a.encryptedStore().Save(a.localDataSnapshot())
}

// updateLocalData stages configuration/quota changes separately from committed
// state. A failed save leaves readers on the previous state. Returning false
// skips the write. Callbacks must copy any nested maps/slices they modify.
func (a *app) updateLocalData(update func(*data) bool) error {
	a.persistenceMu.Lock()
	defer a.persistenceMu.Unlock()
	next := a.localDataSnapshot()
	next.Keys = slices.Clone(next.Keys)
	next.Providers = slices.Clone(next.Providers)
	if !update(&next) {
		return nil
	}
	if err := a.encryptedStore().Save(next); err != nil {
		return err
	}
	a.mu.Lock()
	a.data = next
	a.mu.Unlock()
	return nil
}
