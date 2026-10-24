// Copyright (c) 2026-present Antimatter contributors.
// See LICENSE.txt for license information.

package main

import (
	"bytes"
	"sort"
	"sync"

	"github.com/mattermost/mattermost/server/public/model"
)

// fakeKV is an in-memory kvAPI.
type fakeKV struct {
	mu   sync.Mutex
	data map[string][]byte
	// options are the options a key was last successfully saved with (so a test can check a
	// TTL), keyed the same as data.
	options map[string]model.PluginKVSetOptions
	// writes is every key and value ever saved, in order (so a test can check what was never
	// stored, such as a plain feed token).
	writes []kvWrite
}

type kvWrite struct {
	key   string
	value []byte
}

func newFakeKV() *fakeKV {
	return &fakeKV{data: map[string][]byte{}, options: map[string]model.PluginKVSetOptions{}}
}

func (f *fakeKV) KVGet(key string) ([]byte, *model.AppError) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.data[key]; ok {
		return bytes.Clone(v), nil
	}
	return nil, nil
}

func (f *fakeKV) KVSetWithOptions(key string, value []byte, options model.PluginKVSetOptions) (bool, *model.AppError) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if options.Atomic {
		current, exists := f.data[key]
		if options.OldValue == nil && exists || options.OldValue != nil && (!exists || !bytes.Equal(current, options.OldValue)) {
			return false, nil
		}
	}
	if value == nil {
		delete(f.data, key)
		delete(f.options, key)
	} else {
		f.data[key] = bytes.Clone(value)
		f.options[key] = options
	}
	f.writes = append(f.writes, kvWrite{key: key, value: bytes.Clone(value)})
	return true, nil
}

// allWrites returns a copy of every write so far.
func (f *fakeKV) allWrites() []kvWrite {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]kvWrite(nil), f.writes...)
}

func (f *fakeKV) KVDelete(key string) *model.AppError {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.data, key)
	return nil
}

func (f *fakeKV) KVList(page, perPage int) ([]string, *model.AppError) {
	f.mu.Lock()
	defer f.mu.Unlock()
	keys := make([]string, 0, len(f.data))
	for k := range f.data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	start := min(page*perPage, len(keys))
	end := min(start+perPage, len(keys))
	return keys[start:end], nil
}
