package tests

import (
	"context"
	"sync"
	"time"

	"github.com/rancher/steve/pkg/sqlcache/db"
	"github.com/rancher/steve/pkg/sqlcache/encryption"
	"github.com/rancher/steve/pkg/sqlcache/informer"
	sqlStore "github.com/rancher/steve/pkg/sqlcache/store"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
)

// TestStoreDeleteTombstone reproduces rancher/rancher#55228 end-to-end through
// the real client-go reflector machinery against the real apiserver.
//
// The reflector lists the real apiserver, then runs a watch we control via a
// FakeWatcher. We delete the Banana through the apiserver, then close the
// fake watcher with a 410 Gone error. The reflector exits ListAndWatch and
// the outer loop re-enters it; the fresh List against the apiserver returns
// no items, so DeltaFIFO.Replace synthesizes a Deleted delta wrapping the
// previously-known object in cache.DeletedFinalStateUnknown and processDeltas
// hands it to steve's SQLite Store.Delete.
//
// Today the after-delete hook calls meta.Accessor on the tombstone wrapper,
// which doesn't implement metav1.Object. The error rolls back the surrounding
// SQL transaction, so the row never gets deleted. With the fix, the row goes
// away.
func (i *IntegrationSuite) TestStoreDeleteTombstone() {
	ctx := i.T().Context()

	gvk := schema.GroupVersionKind{Group: "fruits.cattle.io", Version: "v1", Kind: "Banana"}
	gvr := schema.GroupVersionResource{Group: "fruits.cattle.io", Version: "v1", Resource: "bananas"}

	banana := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "fruits.cattle.io/v1",
			"kind":       "Banana",
			"metadata": map[string]interface{}{
				"name": "tombstone-banana",
			},
			"color":  "yellow",
			"number": int64(1),
		},
	}
	i.Require().NoError(i.doApply(ctx, banana, gvr))
	deletedByTest := false
	defer func() {
		if !deletedByTest {
			_ = i.doDelete(ctx, banana, gvr)
		}
	}()

	// Private SQLite DB so we don't collide with any concurrent factory.
	m, err := encryption.NewManager()
	i.Require().NoError(err)
	dbClient, dbPath, err := db.NewClient(ctx, nil, m, m, true /* useTempDir */)
	i.Require().NoError(err)
	i.T().Logf("SQLite cache at %s", dbPath)

	// Build the SharedIndexInformer + SQLite-backed indexer the same way
	// informer.NewInformer does, but with a list/watch source whose List
	// hits the real apiserver and whose Watch hands out a FakeWatcher we
	// control. We need to own the watch because:
	//   - The Banana's real delete event would otherwise reach the reflector
	//     through the live watch and get processed as a normal Delete (no
	//     tombstone).
	//   - We need to deterministically close the watch to force a re-list.
	example := &unstructured.Unstructured{}
	example.SetGroupVersionKind(gvk)

	src := newRealListFakeWatch(ctx, i.client.Resource(gvr))
	sii := cache.NewSharedIndexInformer(
		&noWatchListLW{ListWatch: src.listWatch()},
		example,
		0, // resyncPeriod
		cache.Indexers{},
	)

	name := gvk.Group + "_" + gvk.Version + "_" + gvk.Kind
	store, err := sqlStore.NewStore(
		ctx, example, cache.DeletionHandlingMetaNamespaceKeyFunc,
		dbClient, false /* shouldEncrypt */, gvk, name, nil, nil,
	)
	i.Require().NoError(err)

	loi, err := informer.NewListOptionIndexer(ctx, store, informer.ListOptionIndexerOptions{
		IsNamespaced: false,
		GCKeepCount:  1000,
	})
	i.Require().NoError(err)

	informer.UnsafeSet(sii, "indexer", loi)

	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	go sii.RunWithContext(runCtx)
	i.Require().True(
		cache.WaitForCacheSync(runCtx.Done(), sii.HasSynced),
		"informer never synced",
	)

	const key = "tombstone-banana"
	i.Require().Eventually(func() bool {
		_, exists, err := sii.GetStore().GetByKey(key)
		return err == nil && exists
	}, 10*time.Second, 200*time.Millisecond, "Banana never landed in the SQLite cache")

	// Delete the Banana out from under the reflector — the live watch is
	// fake so the delete event never reaches the reflector.
	i.Require().NoError(i.doDelete(ctx, banana, gvr))
	deletedByTest = true

	// Wait until the reflector has opened its (fake) watch, then close it
	// with a 410-equivalent error. handleWatch returns the error, r.watch
	// returns nil, ListAndWatch returns, the outer Run loop re-enters
	// ListAndWatch and a fresh List hits the apiserver — now empty.
	i.Require().Eventually(func() bool {
		return src.currentWatcher() != nil
	}, 5*time.Second, 50*time.Millisecond, "reflector never opened a watch")

	src.currentWatcher().Error(&metav1.Status{
		Status:  metav1.StatusFailure,
		Reason:  metav1.StatusReasonGone,
		Code:    410,
		Message: "test-induced watch close",
	})

	// The next List returns no Bananas, so DeltaFIFO.Replace generates a
	// Deleted delta with a DeletedFinalStateUnknown wrapper, processDeltas
	// hands it to steve's Store.Delete, and (with the bug) the after-delete
	// hook errors → transaction rolls back → row stays.
	require.Eventually(i.T(), func() bool {
		_, exists, err := sii.GetStore().GetByKey(key)
		return err == nil && !exists
	}, 15*time.Second, 100*time.Millisecond,
		"stale row left in SQLite cache after reflector re-list (tombstone Delete failed)")
}

// realListFakeWatch is a list/watch source whose List hits a real dynamic
// client and whose Watch returns a FakeWatcher the test can drive directly.
type realListFakeWatch struct {
	ctx    context.Context
	client dynamic.ResourceInterface

	mu      sync.Mutex
	watcher *watch.FakeWatcher
}

func newRealListFakeWatch(ctx context.Context, client dynamic.ResourceInterface) *realListFakeWatch {
	return &realListFakeWatch{ctx: ctx, client: client}
}

func (s *realListFakeWatch) currentWatcher() *watch.FakeWatcher {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.watcher
}

func (s *realListFakeWatch) listWatch() *cache.ListWatch {
	return &cache.ListWatch{
		ListFunc: func(opts metav1.ListOptions) (runtime.Object, error) {
			return s.client.List(s.ctx, opts)
		},
		WatchFunc: func(_ metav1.ListOptions) (watch.Interface, error) {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.watcher = watch.NewFake()
			return s.watcher, nil
		},
	}
}

// noWatchListLW disables the WatchList feature, matching informer.NewInformer.
// Without this wrapper the reflector may take the WatchList path and never
// call ListFunc, which breaks the re-list trigger this test depends on.
type noWatchListLW struct {
	*cache.ListWatch
}

func (*noWatchListLW) IsWatchListSemanticsUnSupported() bool { return true }
