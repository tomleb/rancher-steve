package ext

import (
	"context"
	"fmt"

	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
)

type typeChecker struct {
	runtime.Object
}

type typeCheckerList struct {
	runtime.Object
}

// XXX: Implement DeleteCollection to simplify everything here
// var _ rest.StandardStorage = (*delegate[*typeChecker, typeChecker, *typeCheckerList, typeCheckerList])(nil)
var _ rest.Storage = (*delegate[*typeChecker, typeChecker, *typeCheckerList, typeCheckerList])(nil)
var _ rest.Scoper = (*delegate[*typeChecker, typeChecker, *typeCheckerList, typeCheckerList])(nil)
var _ rest.KindProvider = (*delegate[*typeChecker, typeChecker, *typeCheckerList, typeCheckerList])(nil)
var _ rest.GroupVersionKindProvider = (*delegate[*typeChecker, typeChecker, *typeCheckerList, typeCheckerList])(nil)
var _ rest.SingularNameProvider = (*delegate[*typeChecker, typeChecker, *typeCheckerList, typeCheckerList])(nil)

var _ rest.Getter = (*delegate[*typeChecker, typeChecker, *typeCheckerList, typeCheckerList])(nil)
var _ rest.Lister = (*delegate[*typeChecker, typeChecker, *typeCheckerList, typeCheckerList])(nil)
var _ rest.GracefulDeleter = (*delegate[*typeChecker, typeChecker, *typeCheckerList, typeCheckerList])(nil)
var _ rest.Creater = (*delegate[*typeChecker, typeChecker, *typeCheckerList, typeCheckerList])(nil)
var _ rest.Updater = (*delegate[*typeChecker, typeChecker, *typeCheckerList, typeCheckerList])(nil)
var _ rest.Watcher = (*delegate[*typeChecker, typeChecker, *typeCheckerList, typeCheckerList])(nil)

// Ptr[U] acts as a type constraint such that
//
//	T Ptr[U]
//
// means that T is a pointer to U and a runtime.Object.
type Ptr[U any] interface {
	*U
	runtime.Object
}

// delegate is the bridge between k8s.io/apiserver's [rest.Storage] interface and
// our own Store interface we want developers to use
//
// It is used for non-namespaced objects only.
type delegate[
	T Ptr[DerefT],
	DerefT any,
	TList Ptr[DerefTList],
	DerefTList any,
] struct {
	gvk          schema.GroupVersionKind
	gvr          schema.GroupVersionResource
	singularName string
	store        Store[T, TList]
	authorizer   authorizer.Authorizer
}

// New implements [rest.Storage]
//
// It uses generics to create the resource and set its GVK.
func (s *delegate[T, DerefT, TList, DerefTList]) New() runtime.Object {
	var t DerefT
	ptrT := T(&t)
	ptrT.GetObjectKind().SetGroupVersionKind(s.gvk)
	return ptrT
}

// Destroy cleans up its resources on shutdown.
// Destroy has to be implemented in thread-safe way and be prepared
// for being called more than once.
func (s *delegate[T, DerefT, TList, DerefTList]) Destroy() {
}

// NewList implements [rest.Lister]
//
// It uses generics to create the resource and set its GVK.
func (s *delegate[T, DerefT, TList, DerefTList]) NewList() runtime.Object {
	var t DerefTList
	ptrT := TList(&t)
	ptrT.GetObjectKind().SetGroupVersionKind(s.gvk)
	return ptrT
}

// List implements [rest.Lister]
func (s *delegate[T, DerefT, TList, DerefTList]) List(parentCtx context.Context, internaloptions *metainternalversion.ListOptions) (runtime.Object, error) {
	ctx, err := s.makeContext(parentCtx)
	if err != nil {
		return nil, err
	}

	options, err := convertListOptions(internaloptions)
	if err != nil {
		return nil, err
	}

	return s.store.List(ctx, options)
}

// ConvertToTable implements [rest.Lister]
//
// It converts an object or a list of objects to a table, which is used by kubectl
// (and Rancher UI) to display a table of the items.
//
// Currently, we use the default table convertor which will show two columns: Name and Created At.
func (s *delegate[T, DerefT, TList, DerefTList]) ConvertToTable(ctx context.Context, object runtime.Object, tableOptions runtime.Object) (*metav1.Table, error) {
	defaultTableConverter := rest.NewDefaultTableConvertor(s.gvr.GroupResource())
	return defaultTableConverter.ConvertToTable(ctx, object, tableOptions)
}

// Get implements [rest.Getter]
func (s *delegate[T, DerefT, TList, DerefTList]) Get(parentCtx context.Context, name string, options *metav1.GetOptions) (runtime.Object, error) {
	ctx, err := s.makeContext(parentCtx)
	if err != nil {
		return nil, err
	}

	return s.store.Get(ctx, name, options)
}

// Delete implements [rest.GracefulDeleter]
func (s *delegate[T, DerefT, TList, DerefTList]) Delete(parentCtx context.Context, name string, deleteValidation rest.ValidateObjectFunc, options *metav1.DeleteOptions) (runtime.Object, bool, error) {
	ctx, err := s.makeContext(parentCtx)
	if err != nil {
		return nil, false, err
	}

	err = s.store.Delete(ctx, name, options)
	return nil, true, err
}

// Create implements [rest.Creater]
func (s *delegate[T, DerefT, TList, DerefTList]) Create(parentCtx context.Context, obj runtime.Object, createValidation rest.ValidateObjectFunc, options *metav1.CreateOptions) (runtime.Object, error) {
	ctx, err := s.makeContext(parentCtx)
	if err != nil {
		return nil, err
	}

	if createValidation != nil {
		err := createValidation(ctx, obj)
		if err != nil {
			return obj, err
		}
	}

	return s.store.Create(ctx, obj.(T), options)
}

// Update implements [rest.Updater]
func (s *delegate[T, DerefT, TList, DerefTList]) Update(
	parentCtx context.Context,
	name string,
	objInfo rest.UpdatedObjectInfo,
	createValidation rest.ValidateObjectFunc,
	updateValidation rest.ValidateObjectUpdateFunc,
	forceAllowCreate bool,
	options *metav1.UpdateOptions,
) (runtime.Object, bool, error) {
	ctx, err := s.makeContext(parentCtx)
	if err != nil {
		return nil, false, err
	}

	oldObj, err := s.store.Get(ctx, name, &metav1.GetOptions{})
	if err != nil {
		// XXX: Do we want to support creation??
		return nil, false, err
	}

	newObj, err := objInfo.UpdatedObject(ctx, oldObj)
	if err != nil {
		return nil, false, err
	}

	newT, ok := newObj.(T)
	if !ok {
		return nil, false, fmt.Errorf("wrong expected type")
	}

	if updateValidation != nil {
		err = updateValidation(ctx, newT, oldObj)
		if err != nil {
			return nil, false, err
		}
	}

	newT, err = s.store.Update(ctx, newT, options)
	if err != nil {
		return nil, false, err
	}

	return newT, false, nil
}

type watcher struct {
	ch chan watch.Event
}

func (w *watcher) Stop() {
	close(w.ch)
}

func (w *watcher) ResultChan() <-chan watch.Event {
	return w.ch
}

func (s *delegate[T, DerefT, TList, DerefTList]) Watch(parentCtx context.Context, options *metainternalversion.ListOptions) (watch.Interface, error) {
	ctx, err := s.makeContext(parentCtx)
	if err != nil {
		return nil, err
	}

	w := &watcher{
		ch: make(chan watch.Event),
	}
	go func() {
		eventCh, err := s.store.Watch(ctx, &metav1.ListOptions{})
		if err != nil {
			return
		}
		// defer close(eventCh)

		for event := range eventCh {
			w.ch <- watch.Event{
				Type:   event.Event,
				Object: event.Object,
			}
		}
	}()

	return w, nil
}

// GroupVersionKind implements rest.GroupVersionKind
//
// This is used to generate the data for the Discovery API
func (s *delegate[T, DerefT, TList, DerefTList]) GroupVersionKind(_ schema.GroupVersion) schema.GroupVersionKind {
	return s.gvk
}

// NamespaceScoped implements rest.Scoper
//
// The delegate is used for non-namespaced resources so it always returns false
func (s *delegate[T, DerefT, TList, DerefTList]) NamespaceScoped() bool {
	return false
}

// Kind implements rest.KindProvider
//
// XXX: Example where / how this is used
func (s *delegate[T, DerefT, TList, DerefTList]) Kind() string {
	return s.gvk.Kind
}

// GetSingularName implements rest.SingularNameProvider
//
// This is used by a variety of things such as kubectl to map singular name to
// resource name. (eg: token => tokens)
func (s *delegate[T, DerefT, TList, DerefTList]) GetSingularName() string {
	return s.singularName
}

func (s *delegate[T, DerefT, TList, DerefTList]) makeContext(parentCtx context.Context) (Context, error) {
	userInfo, ok := request.UserFrom(parentCtx)
	if !ok {
		return Context{}, fmt.Errorf("missing user info")
	}

	ctx := Context{
		Context:    parentCtx,
		User:       userInfo,
		Authorizer: s.authorizer,
	}
	return ctx, nil
}

func convertListOptions(options *metainternalversion.ListOptions) (*metav1.ListOptions, error) {
	var out metav1.ListOptions
	err := scheme.Convert(options, &out, nil)
	if err != nil {
		return nil, fmt.Errorf("convert list options: %w", err)
	}

	return &out, nil
}
