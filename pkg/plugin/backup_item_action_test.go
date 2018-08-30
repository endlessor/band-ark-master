/*
Copyright 2018 the Heptio Ark contributors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package plugin

import (
	"encoding/json"
	"testing"

	"github.com/heptio/ark/pkg/apis/ark/v1"
	"github.com/heptio/ark/pkg/backup"
	"github.com/heptio/ark/pkg/backup/mocks"
	proto "github.com/heptio/ark/pkg/plugin/generated"
	arktest "github.com/heptio/ark/pkg/util/test"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestBackupItemActionGRPCServerExecute(t *testing.T) {
	invalidItem := []byte("this is gibberish json")
	validItem := []byte(`
	{
		"apiVersion": "v1",
		"kind": "ConfigMap",
		"metadata": {
			"namespace": "myns",
			"name": "myconfigmap"
		},
		"data": {
			"key": "value"
		}
	}`)
	var validItemObject unstructured.Unstructured
	err := json.Unmarshal(validItem, &validItemObject)
	require.NoError(t, err)

	updatedItem := []byte(`
		{
			"apiVersion": "v1",
			"kind": "ConfigMap",
			"metadata": {
				"namespace": "myns",
				"name": "myconfigmap"
			},
			"data": {
				"key": "changed!"
			}
		}`)
	var updatedItemObject unstructured.Unstructured
	err = json.Unmarshal(updatedItem, &updatedItemObject)
	require.NoError(t, err)

	invalidBackup := []byte("this is gibberish json")
	validBackup := []byte(`
	{
		"apiVersion": "ark.heptio.com/v1",
		"kind": "Backup",
		"metadata": {
			"namespace": "myns",
			"name": "mybackup"
		},
		"spec": {
			"includedNamespaces": ["*"],
			"includedResources": ["*"],
			"ttl": "60m"
		}
	}`)
	var validBackupObject v1.Backup
	err = json.Unmarshal(validBackup, &validBackupObject)
	require.NoError(t, err)

	tests := []struct {
		name                string
		backup              []byte
		item                []byte
		implUpdatedItem     runtime.Unstructured
		implAdditionalItems []backup.ResourceIdentifier
		implError           error
		expectError         bool
		skipMock            bool
	}{
		{
			name:        "error unmarshaling item",
			item:        invalidItem,
			backup:      validBackup,
			expectError: true,
			skipMock:    true,
		},
		{
			name:        "error unmarshaling backup",
			item:        validItem,
			backup:      invalidBackup,
			expectError: true,
			skipMock:    true,
		},
		{
			name:        "error running impl",
			item:        validItem,
			backup:      validBackup,
			implError:   errors.New("impl error"),
			expectError: true,
		},
		{
			name:   "nil updatedItem / no additionalItems",
			item:   validItem,
			backup: validBackup,
		},
		{
			name:            "same updatedItem / some additionalItems",
			item:            validItem,
			backup:          validBackup,
			implUpdatedItem: &validItemObject,
			implAdditionalItems: []backup.ResourceIdentifier{
				{
					GroupResource: schema.GroupResource{Group: "v1", Resource: "pods"},
					Namespace:     "myns",
					Name:          "mypod",
				},
			},
		},
		{
			name:            "different updatedItem",
			item:            validItem,
			backup:          validBackup,
			implUpdatedItem: &updatedItemObject,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			itemAction := &mocks.ItemAction{}
			defer itemAction.AssertExpectations(t)

			if !test.skipMock {
				itemAction.On("Execute", &validItemObject, &validBackupObject).Return(test.implUpdatedItem, test.implAdditionalItems, test.implError)
			}

			s := &BackupItemActionGRPCServer{mux: &serverMux{
				serverLog: arktest.NewLogger(),
				handlers: map[string]interface{}{
					"xyz": itemAction,
				},
			}}

			req := &proto.ExecuteRequest{
				Plugin: "xyz",
				Item:   test.item,
				Backup: test.backup,
			}

			resp, err := s.Execute(context.Background(), req)

			// Verify error
			assert.Equal(t, test.expectError, err != nil)
			if err != nil {
				return
			}
			require.NotNil(t, resp)

			// Verify updated item
			updatedItem := test.implUpdatedItem
			if updatedItem == nil {
				// If the impl returned nil for its updatedItem, we should expect the plugin to return the original item
				updatedItem = &validItemObject
			}

			var respItem unstructured.Unstructured
			err = json.Unmarshal(resp.Item, &respItem)
			require.NoError(t, err)

			assert.Equal(t, updatedItem, &respItem)

			// Verify additional items
			var expectedAdditionalItems []*proto.ResourceIdentifier
			for _, item := range test.implAdditionalItems {
				expectedAdditionalItems = append(expectedAdditionalItems, backupResourceIdentifierToProto(item))
			}
			assert.Equal(t, expectedAdditionalItems, resp.AdditionalItems)
		})
	}
}
