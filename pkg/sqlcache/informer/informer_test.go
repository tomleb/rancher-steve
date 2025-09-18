package informer

import (
	"context"
	"fmt"
	"testing"

	"github.com/rancher/steve/pkg/sqlcache/partition"
	"github.com/rancher/steve/pkg/sqlcache/sqltypes"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

//go:generate mockgen --build_flags=--mod=mod -package informer -destination ./informer_mocks_test.go github.com/rancher/steve/pkg/sqlcache/informer ByOptionsLister
//go:generate mockgen --build_flags=--mod=mod -package informer -destination ./dynamic_mocks_test.go k8s.io/client-go/dynamic ResourceInterface

func TestInformerListByOptions(t *testing.T) {
	type testCase struct {
		description string
		test        func(t *testing.T)
	}

	var tests []testCase

	tests = append(tests, testCase{description: "ListByOptions() with no errors returned, should return no error and return value from indexer's ListByOptions()", test: func(t *testing.T) {
		indexer := NewMockByOptionsLister(gomock.NewController(t))
		informer := &Informer{
			ByOptionsLister: indexer,
		}
		lo := sqltypes.ListOptions{}
		var partitions []partition.Partition
		ns := "somens"
		expectedList := &unstructured.UnstructuredList{
			Object: map[string]interface{}{"s": 2},
			Items: []unstructured.Unstructured{{
				Object: map[string]interface{}{"s": 2},
			}},
		}
		expectedTotal := len(expectedList.Items)
		expectedContinueToken := "123"
		indexer.EXPECT().ListByOptions(context.Background(), &lo, partitions, ns).Return(expectedList, expectedTotal, expectedContinueToken, nil)
		list, total, continueToken, err := informer.ListByOptions(context.Background(), &lo, partitions, ns)
		assert.Nil(t, err)
		assert.Equal(t, expectedList, list)
		assert.Equal(t, len(expectedList.Items), total)
		assert.Equal(t, expectedContinueToken, continueToken)
	}})
	tests = append(tests, testCase{description: "ListByOptions() with indexer ListByOptions error, should return error", test: func(t *testing.T) {
		indexer := NewMockByOptionsLister(gomock.NewController(t))
		informer := &Informer{
			ByOptionsLister: indexer,
		}
		lo := sqltypes.ListOptions{}
		var partitions []partition.Partition
		ns := "somens"
		indexer.EXPECT().ListByOptions(context.Background(), &lo, partitions, ns).Return(nil, 0, "", fmt.Errorf("error"))
		_, _, _, err := informer.ListByOptions(context.Background(), &lo, partitions, ns)
		assert.NotNil(t, err)
	}})
	t.Parallel()
	for _, test := range tests {
		t.Run(test.description, func(t *testing.T) { test.test(t) })
	}
}
