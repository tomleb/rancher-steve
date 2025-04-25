package main

import (
	"context"
	"fmt"
	"sort"
	"sync"

	extv1 "github.com/rancher/steve/cmd/example/apis/ext.cattle.io/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/registry/rest"
)

var (
	testTypeListFixture = extv1.TestTypeList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "TestTypeList",
			APIVersion: extv1.SchemeGroupVersion.String(),
		},
		Items: []extv1.TestType{
			testTypeFixture,
		},
	}

	testTypeFixture = extv1.TestType{
		TypeMeta: metav1.TypeMeta{
			Kind:       "TestType",
			APIVersion: extv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "foo",
		},
	}
)

var _ rest.Storage = (*testStore[*extv1.TestType, *extv1.TestTypeList])(nil)
var _ rest.Lister = (*testStore[*extv1.TestType, *extv1.TestTypeList])(nil)
var _ rest.GracefulDeleter = (*testStore[*extv1.TestType, *extv1.TestTypeList])(nil)
var _ rest.Creater = (*testStore[*extv1.TestType, *extv1.TestTypeList])(nil)
var _ rest.Updater = (*testStore[*extv1.TestType, *extv1.TestTypeList])(nil)
var _ rest.Getter = (*testStore[*extv1.TestType, *extv1.TestTypeList])(nil)

type testStore[T runtime.Object, TList runtime.Object] struct {
	singular string
	objT     T
	objListT TList
	gvk      schema.GroupVersionKind
	gvr      schema.GroupVersionResource

	// lock protects both items and watcher
	lock    sync.Mutex
	items   map[string]*extv1.TestType
	watcher *watcher
}

func NewDefaultTestStore() *testStore[*extv1.TestType, *extv1.TestTypeList] {
	return &testStore[*extv1.TestType, *extv1.TestTypeList]{
		singular: "testtype",
		objT:     &extv1.TestType{},
		objListT: &extv1.TestTypeList{},
		gvk:      extv1.SchemeGroupVersion.WithKind("TestType"),
		gvr:      extv1.SchemeGroupVersion.WithResource(extv1.TestTypeResourceName),
		items: map[string]*extv1.TestType{
			testTypeFixture.Name: &testTypeFixture,
		},
	}
}

// New implements [rest.Storage]
func (t *testStore[T, TList]) New() runtime.Object {
	obj := t.objT.DeepCopyObject()
	obj.GetObjectKind().SetGroupVersionKind(t.gvk)
	return obj
}

// GetSingularName implements [rest.SingularNameProvider]
func (t *testStore[T, TList]) GetSingularName() string {
	return t.singular
}

// NamespaceScoped implements [rest.Scoper]
func (t *testStore[T, TList]) NamespaceScoped() bool {
	return false
}

// GroupVersionKind implements [rest.GroupVersionKindProvider]
func (t *testStore[T, TList]) GroupVersionKind(_ schema.GroupVersion) schema.GroupVersionKind {
	return t.gvk
}

// Destroy implements [rest.Storage]
func (t *testStore[T, TList]) Destroy() {
}

// Get implements [rest.Getter]
func (t *testStore[T, TList]) Get(ctx context.Context, name string, options *metav1.GetOptions) (runtime.Object, error) {
	t.lock.Lock()
	defer t.lock.Unlock()

	return t.get(ctx, name, options)
}

// Create implements [rest.Creater]
func (t *testStore[T, TList]) Create(ctx context.Context, obj runtime.Object, createValidation rest.ValidateObjectFunc, options *metav1.CreateOptions) (runtime.Object, error) {
	t.lock.Lock()
	defer t.lock.Unlock()

	if createValidation != nil {
		err := createValidation(ctx, obj)
		if err != nil {
			return obj, err
		}
	}

	objT, ok := obj.(*extv1.TestType)
	if !ok {
		var zeroT T
		return nil, fmt.Errorf("expected %T but got %T", zeroT, obj)
	}

	return t.create(ctx, objT, options)
}

// Update implements [rest.Updater]
func (t *testStore[T, TList]) Update(ctx context.Context, name string, objInfo rest.UpdatedObjectInfo, createValidation rest.ValidateObjectFunc, updateValidation rest.ValidateObjectUpdateFunc, forceAllowCreate bool, options *metav1.UpdateOptions) (runtime.Object, bool, error) {
	t.lock.Lock()
	defer t.lock.Unlock()
	return CreateOrUpdate(ctx, name, objInfo, createValidation, updateValidation, forceAllowCreate, options, t.get, t.create, t.update)
}

func (t *testStore[T, TList]) get(_ context.Context, name string, _ *metav1.GetOptions) (*extv1.TestType, error) {
	obj, found := t.items[name]
	if !found {
		return nil, apierrors.NewNotFound(t.gvr.GroupResource(), name)
	}
	return obj, nil
}

func (t *testStore[T, TList]) create(_ context.Context, obj *extv1.TestType, _ *metav1.CreateOptions) (*extv1.TestType, error) {
	if _, found := t.items[obj.Name]; found {
		return nil, apierrors.NewAlreadyExists(t.gvr.GroupResource(), obj.Name)
	}
	t.items[obj.Name] = obj
	t.addEventLocked(watch.Event{
		Type:   watch.Added,
		Object: obj,
	})
	return obj, nil
}

func (t *testStore[T, TList]) update(_ context.Context, obj *extv1.TestType, _ *metav1.UpdateOptions) (*extv1.TestType, error) {
	if _, found := t.items[obj.Name]; !found {
		return nil, apierrors.NewNotFound(t.gvr.GroupResource(), obj.Name)
	}
	obj.ManagedFields = []metav1.ManagedFieldsEntry{}
	t.items[obj.Name] = obj
	t.addEventLocked(watch.Event{
		Type:   watch.Modified,
		Object: obj,
	})
	return obj, nil
}

// NewList implements [rest.Lister]
func (t *testStore[T, TList]) NewList() runtime.Object {
	objList := t.objListT.DeepCopyObject()
	objList.GetObjectKind().SetGroupVersionKind(t.gvk)
	return objList
}

// List implements [rest.Lister]
func (t *testStore[T, TList]) List(ctx context.Context, options *metainternalversion.ListOptions) (runtime.Object, error) {
	t.lock.Lock()
	defer t.lock.Unlock()

	items := []extv1.TestType{}
	for _, obj := range t.items {
		items = append(items, *obj)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Name > items[j].Name
	})
	list := &extv1.TestTypeList{
		Items: items,
	}
	return list, nil
}

// ConvertToTable implements [rest.Lister]
func (t *testStore[T, TList]) ConvertToTable(ctx context.Context, object runtime.Object, tableOptions runtime.Object) (*metav1.Table, error) {
	return ConvertToTableDefault[T](ctx, object, tableOptions, t.gvr.GroupResource())
}

// Watch implements [rest.Watcher]
func (t *testStore[T, TList]) Watch(ctx context.Context, internaloptions *metainternalversion.ListOptions) (watch.Interface, error) {
	t.lock.Lock()
	defer t.lock.Unlock()

	w := &watcher{
		ch: make(chan watch.Event, 100),
	}
	t.watcher = w
	return w, nil
}

func (t *testStore[T, TList]) addEventLocked(event watch.Event) {
	if t.watcher != nil {
		t.watcher.addEvent(event)
	}
}

// Delete implements [rest.GracefulDeleter]
func (t *testStore[T, TList]) Delete(ctx context.Context, name string, deleteValidation rest.ValidateObjectFunc, options *metav1.DeleteOptions) (runtime.Object, bool, error) {
	t.lock.Lock()
	defer t.lock.Unlock()

	obj, found := t.items[name]
	if !found {
		return nil, false, apierrors.NewNotFound(t.gvr.GroupResource(), name)
	}

	if deleteValidation != nil {
		err := deleteValidation(ctx, obj)
		if err != nil {
			return nil, false, err
		}
	}

	delete(t.items, name)
	t.addEventLocked(watch.Event{
		Type:   watch.Deleted,
		Object: obj,
	})
	return obj, true, nil
}

type watcher struct {
	closedLock sync.RWMutex
	closed     bool
	ch         chan watch.Event
}

// Stop implements [watch.Interface]
//
// As documented, Stop must only be called by the consumer (the k8s library) not the producer (our store)
func (w *watcher) Stop() {
	w.closedLock.Lock()
	defer w.closedLock.Unlock()
	if !w.closed {
		close(w.ch)
		w.closed = true
	}
}

// ResultChan implements [watch.Interface]
func (w *watcher) ResultChan() <-chan watch.Event {
	return w.ch
}

func (w *watcher) addEvent(event watch.Event) bool {
	w.closedLock.RLock()
	defer w.closedLock.RUnlock()
	if w.closed {
		return false
	}

	w.ch <- event
	return true
}

// ConvertFunc will convert an object to a list of cell in a metav1.Table (think kubectl get table output)
type ConvertFunc[T runtime.Object] func(obj T) []string

// ConvertToTable helps implement [rest.Lister] and [rest.TableConvertor].
//
// It converts an object or a list of objects to a Table, which is used by kubectl
// (and Rancher UI) to display a table of the items.
func ConvertToTable[T runtime.Object](ctx context.Context, object runtime.Object, tableOptions runtime.Object, groupResource schema.GroupResource, columnDefs []metav1.TableColumnDefinition, convertFn ConvertFunc[T]) (*metav1.Table, error) {
	result, err := convertToTable(ctx, object, tableOptions, groupResource, columnDefs, convertFn)
	if err != nil {
		return nil, convertError(err)
	}
	return result, nil
}

// ConvertToTableDefault helps implement [rest.Lister] and [rest.TableConvertor].
//
// This uses the default table conversion that displays the following two
// columns: Name and Created At.
func ConvertToTableDefault[T runtime.Object](ctx context.Context, object runtime.Object, tableOptions runtime.Object, groupResource schema.GroupResource) (*metav1.Table, error) {
	return ConvertToTable[T](ctx, object, tableOptions, groupResource, nil, nil)
}

func convertToTable[T runtime.Object](ctx context.Context, object runtime.Object, tableOptions runtime.Object, groupResource schema.GroupResource, columnDefs []metav1.TableColumnDefinition, convertFn ConvertFunc[T]) (*metav1.Table, error) {
	defaultTableConverter := rest.NewDefaultTableConvertor(groupResource)
	table, err := defaultTableConverter.ConvertToTable(ctx, object, tableOptions)
	if err != nil {
		return nil, err
	}

	if columnDefs == nil {
		return table, nil
	}

	// Override only if there were definitions before (to respect the NoHeader option)
	if len(table.ColumnDefinitions) > 0 {
		table.ColumnDefinitions = columnDefs
	}
	table.Rows = []metav1.TableRow{}
	fn := func(obj runtime.Object) error {
		objT, ok := obj.(T)
		if !ok {
			var zeroT T
			return fmt.Errorf("expected %T but got %T", zeroT, obj)
		}
		cells := convertFn(objT)
		if len(cells) != len(columnDefs) {
			return fmt.Errorf("defined %d columns but got %d cells", len(columnDefs), len(cells))
		}

		table.Rows = append(table.Rows, metav1.TableRow{
			Cells:  cellStringToCellAny(cells),
			Object: runtime.RawExtension{Object: obj},
		})
		return nil
	}
	switch {
	case meta.IsListType(object):
		if err := meta.EachListItem(object, fn); err != nil {
			return nil, err
		}
	default:
		if err := fn(object); err != nil {
			return nil, err
		}
	}

	return table, nil
}

func cellStringToCellAny(cells []string) []any {
	var res []any
	for _, cell := range cells {
		res = append(res, cell)
	}
	return res
}

// CreateOrUpdate helps implement [rest.Updater] by handling most of the logic.
//
// It will call getFn to find the object. If not found, then createFn will
// be called, which should create the object. Otherwise, the updateFn will be called,
// which should update the object.
//
// createValidation is called before createFn. It will do validation such as:
//   - verifying that the user is allowed to by checking for the "create" verb.
//     See here for details: https://github.com/kubernetes/apiserver/blob/70ed6fdbea9eb37bd1d7558e90c20cfe888955e8/pkg/endpoints/handlers/update.go#L190-L201
//   - running mutating/validating webhooks (though we're not using them yet)
//
// updateValidation is called before updateFn. It will do validation such as:
// - running mutating/validating webhooks (though we're not using them yet)
func CreateOrUpdate[T runtime.Object](
	ctx context.Context,
	name string,
	objInfo rest.UpdatedObjectInfo,
	createValidation rest.ValidateObjectFunc,
	updateValidation rest.ValidateObjectUpdateFunc,
	forceAllowCreate bool,
	options *metav1.UpdateOptions,
	getFn func(ctx context.Context, name string, opts *metav1.GetOptions) (T, error),
	createFn func(ctx context.Context, obj T, opts *metav1.CreateOptions) (T, error),
	updateFn func(ctx context.Context, obj T, opts *metav1.UpdateOptions) (T, error),
) (runtime.Object, bool, error) {
	oldObj, err := getFn(ctx, name, &metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, false, err
		}

		obj, err := objInfo.UpdatedObject(ctx, nil)
		if err != nil {
			return nil, false, convertError(err)
		}

		if err = createValidation(ctx, obj); err != nil {
			return nil, false, convertError(err)
		}

		tObj, ok := obj.(T)
		if !ok {
			var zeroT T
			return nil, false, convertError(fmt.Errorf("object was of type %T, not of expected type %T", obj, zeroT))
		}

		newObj, err := createFn(ctx, tObj, &metav1.CreateOptions{})
		if err != nil {
			return nil, false, convertError(err)
		}
		return newObj, true, nil
	}

	newObj, err := objInfo.UpdatedObject(ctx, oldObj)
	if err != nil {
		return nil, false, convertError(err)
	}

	newT, ok := newObj.(T)
	if !ok {
		var zeroT T
		return nil, false, convertError(fmt.Errorf("object was of type %T, not of expected type %T", newObj, zeroT))
	}

	if updateValidation != nil {
		err = updateValidation(ctx, newT, oldObj)
		if err != nil {
			return nil, false, convertError(err)
		}
	}

	newT, err = updateFn(ctx, newT, options)
	if err != nil {
		return nil, false, err
	}

	return newT, false, nil
}

// ConvertListOptions converts an internal ListOptions to one used by client-go.
//
// This can be useful if wrapping Watch or List methods to client-go's equivalent.
func ConvertListOptions(options *metainternalversion.ListOptions) (*metav1.ListOptions, error) {
	scheme := sync.OnceValue(func() *runtime.Scheme {
		scheme := runtime.NewScheme()
		metainternalversion.AddToScheme(scheme)
		return scheme
	})()

	var out metav1.ListOptions
	err := scheme.Convert(options, &out, nil)
	if err != nil {
		return nil, fmt.Errorf("converting list options: %w", err)
	}

	return &out, nil
}

func convertError(err error) error {
	if _, ok := err.(apierrors.APIStatus); ok {
		return err
	}

	return apierrors.NewInternalError(err)
}
