package lua

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/argoproj/gitops-engine/pkg/health"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	lua "github.com/yuin/gopher-lua"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	applicationpkg "github.com/argoproj/argo-cd/v3/pkg/apiclient/application"
	appv1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/argoproj/argo-cd/v3/util/grpc"
)

const objJSON = `
apiVersion: argoproj.io/v1alpha1
kind: Rollout
metadata:
  labels:
    app.kubernetes.io/instance: helm-guestbook
  name: helm-guestbook
  namespace: default
  resourceVersion: "123"
`

const objWithNoScriptJSON = `
apiVersion: not-an-endpoint.io/v1alpha1
kind: Test
metadata:
  labels:
    app.kubernetes.io/instance: helm-guestbook
  name: helm-guestbook
  namespace: default
  resourceVersion: "123"
`

const ec2AWSCrossplaneObjJSON = `
apiVersion: ec2.aws.crossplane.io/v1alpha1
kind: Instance
metadata:
  name: sample-crosspalne-ec2-instance
spec:
  forProvider:
    region: us-west-2
    instanceType: t2.micro
    keyName: my-crossplane-key-pair
  providerConfigRef:
    name: awsconfig
`

const newHealthStatusFunction = `a = {}
a.status = "Healthy"
a.message ="NeedsToBeChanged"
if obj.metadata.name == "helm-guestbook" then
	a.message = "testMessage"
end
return a`

const newWildcardHealthStatusFunction = `a = {}
a.status = "Healthy"
a.message ="NeedsToBeChanged"
if obj.metadata.name == "sample-crosspalne-ec2-instance" then
	a.message = "testWildcardMessage"
end
return a`

func StrToUnstructured(jsonStr string) *unstructured.Unstructured {
	obj := make(map[string]any)
	err := yaml.Unmarshal([]byte(jsonStr), &obj)
	if err != nil {
		panic(err)
	}
	return &unstructured.Unstructured{Object: obj}
}

func TestExecuteNewHealthStatusFunction(t *testing.T) {
	testObj := StrToUnstructured(objJSON)
	vm := VM{}
	status, err := vm.ExecuteHealthLua(testObj, newHealthStatusFunction)
	require.NoError(t, err)
	expectedHealthStatus := &health.HealthStatus{
		Status:  "Healthy",
		Message: "testMessage",
	}
	assert.Equal(t, expectedHealthStatus, status)
}

func TestExecuteWildcardHealthStatusFunction(t *testing.T) {
	testObj := StrToUnstructured(ec2AWSCrossplaneObjJSON)
	vm := VM{}
	status, err := vm.ExecuteHealthLua(testObj, newWildcardHealthStatusFunction)
	require.NoError(t, err)
	expectedHealthStatus := &health.HealthStatus{
		Status:  "Healthy",
		Message: "testWildcardMessage",
	}
	assert.Equal(t, expectedHealthStatus, status)
}

const osLuaScript = `os.getenv("HOME")`

func TestFailExternalLibCall(t *testing.T) {
	testObj := StrToUnstructured(objJSON)
	vm := VM{}
	_, err := vm.ExecuteHealthLua(testObj, osLuaScript)
	require.Error(t, err)
	assert.IsType(t, &lua.ApiError{}, err)
}

const returnInt = `return 1`

func TestFailLuaReturnNonTable(t *testing.T) {
	testObj := StrToUnstructured(objJSON)
	vm := VM{}
	_, err := vm.ExecuteHealthLua(testObj, returnInt)
	assert.Equal(t, fmt.Errorf(incorrectReturnType, "table", "number"), err)
}

const invalidHealthStatusStatus = `local healthStatus = {}
healthStatus.status = "test"
return healthStatus
`

func TestInvalidHealthStatusStatus(t *testing.T) {
	testObj := StrToUnstructured(objJSON)
	vm := VM{}
	status, err := vm.ExecuteHealthLua(testObj, invalidHealthStatusStatus)
	require.NoError(t, err)
	expectedStatus := &health.HealthStatus{
		Status:  health.HealthStatusUnknown,
		Message: invalidHealthStatus,
	}
	assert.Equal(t, expectedStatus, status)
}

const validReturnNothingHealthStatusStatus = `local healthStatus = {}
return
`

func TestNoReturnHealthStatusStatus(t *testing.T) {
	testObj := StrToUnstructured(objJSON)
	vm := VM{}
	status, err := vm.ExecuteHealthLua(testObj, validReturnNothingHealthStatusStatus)
	require.NoError(t, err)
	expectedStatus := &health.HealthStatus{}
	assert.Equal(t, expectedStatus, status)
}

const validNilHealthStatusStatus = `local healthStatus = {}
return nil
`

func TestNilHealthStatusStatus(t *testing.T) {
	testObj := StrToUnstructured(objJSON)
	vm := VM{}
	status, err := vm.ExecuteHealthLua(testObj, validNilHealthStatusStatus)
	require.NoError(t, err)
	expectedStatus := &health.HealthStatus{}
	assert.Equal(t, expectedStatus, status)
}

const validEmptyArrayHealthStatusStatus = `local healthStatus = {}
return healthStatus
`

func TestEmptyHealthStatusStatus(t *testing.T) {
	testObj := StrToUnstructured(objJSON)
	vm := VM{}
	status, err := vm.ExecuteHealthLua(testObj, validEmptyArrayHealthStatusStatus)
	require.NoError(t, err)
	expectedStatus := &health.HealthStatus{}
	assert.Equal(t, expectedStatus, status)
}

const infiniteLoop = `while true do ; end`

func TestHandleInfiniteLoop(t *testing.T) {
	testObj := StrToUnstructured(objJSON)
	vm := VM{}
	_, err := vm.ExecuteHealthLua(testObj, infiniteLoop)
	assert.IsType(t, &lua.ApiError{}, err)
}

func TestGetHealthScriptWithOverride(t *testing.T) {
	testObj := StrToUnstructured(objJSON)
	vm := VM{
		ResourceOverrides: map[string]appv1.ResourceOverride{
			"argoproj.io/Rollout": {
				HealthLua:   newHealthStatusFunction,
				UseOpenLibs: false,
			},
		},
	}
	script, useOpenLibs, err := vm.GetHealthScript(testObj)
	require.NoError(t, err)
	assert.False(t, useOpenLibs)
	assert.Equal(t, newHealthStatusFunction, script)
}

func TestGetHealthScriptWithKindWildcardOverride(t *testing.T) {
	testObj := StrToUnstructured(objJSON)
	vm := VM{
		ResourceOverrides: map[string]appv1.ResourceOverride{
			"argoproj.io/*": {
				HealthLua:   newHealthStatusFunction,
				UseOpenLibs: false,
			},
		},
	}

	script, useOpenLibs, err := vm.GetHealthScript(testObj)
	require.NoError(t, err)
	assert.False(t, useOpenLibs)
	assert.Equal(t, newHealthStatusFunction, script)
}

func TestGetHealthScriptWithGroupWildcardOverride(t *testing.T) {
	testObj := StrToUnstructured(objJSON)
	vm := VM{
		ResourceOverrides: map[string]appv1.ResourceOverride{
			"*.io/Rollout": {
				HealthLua:   newHealthStatusFunction,
				UseOpenLibs: false,
			},
		},
	}

	script, useOpenLibs, err := vm.GetHealthScript(testObj)
	require.NoError(t, err)
	assert.False(t, useOpenLibs)
	assert.Equal(t, newHealthStatusFunction, script)
}

func TestGetHealthScriptWithGroupAndKindWildcardOverride(t *testing.T) {
	testObj := StrToUnstructured(ec2AWSCrossplaneObjJSON)
	vm := VM{
		ResourceOverrides: map[string]appv1.ResourceOverride{
			"*.aws.crossplane.io/*": {
				HealthLua:   newHealthStatusFunction,
				UseOpenLibs: false,
			},
		},
	}

	script, useOpenLibs, err := vm.GetHealthScript(testObj)
	require.NoError(t, err)
	assert.False(t, useOpenLibs)
	assert.Equal(t, newHealthStatusFunction, script)
}

func TestGetHealthScriptPredefined(t *testing.T) {
	testObj := StrToUnstructured(objJSON)
	vm := VM{}
	script, useOpenLibs, err := vm.GetHealthScript(testObj)
	require.NoError(t, err)
	assert.True(t, useOpenLibs)
	assert.NotEmpty(t, script)
}

func TestGetHealthScriptNoPredefined(t *testing.T) {
	testObj := StrToUnstructured(objWithNoScriptJSON)
	vm := VM{}
	script, useOpenLibs, err := vm.GetHealthScript(testObj)
	require.NoError(t, err)
	assert.False(t, useOpenLibs)
	assert.Empty(t, script)
}

func TestGetResourceActionPredefined(t *testing.T) {
	testObj := StrToUnstructured(objJSON)
	vm := VM{}

	action, err := vm.GetResourceAction(testObj, "resume")
	require.NoError(t, err)
	assert.NotEmpty(t, action)
}

func TestGetResourceActionNoPredefined(t *testing.T) {
	testObj := StrToUnstructured(objWithNoScriptJSON)
	vm := VM{}
	action, err := vm.GetResourceAction(testObj, "test")
	require.ErrorIs(t, err, errScriptDoesNotExist)
	assert.Empty(t, action.ActionLua)
}

func TestGetResourceActionWithOverride(t *testing.T) {
	testObj := StrToUnstructured(objJSON)
	test := appv1.ResourceActionDefinition{
		Name:      "test",
		ActionLua: "return obj",
	}

	vm := VM{
		ResourceOverrides: map[string]appv1.ResourceOverride{
			"argoproj.io/Rollout": {
				Actions: string(grpc.MustMarshal(appv1.ResourceActions{
					Definitions: []appv1.ResourceActionDefinition{
						test,
					},
				})),
			},
		},
	}
	action, err := vm.GetResourceAction(testObj, "test")
	require.NoError(t, err)
	assert.Equal(t, test, action)
}

func TestGetResourceActionDiscoveryPredefined(t *testing.T) {
	testObj := StrToUnstructured(objJSON)
	vm := VM{}

	discoveryLua, err := vm.GetResourceActionDiscovery(testObj)
	require.NoError(t, err)
	assert.NotEmpty(t, discoveryLua)
}

func TestGetResourceActionDiscoveryNoPredefined(t *testing.T) {
	testObj := StrToUnstructured(objWithNoScriptJSON)
	vm := VM{}
	discoveryLua, err := vm.GetResourceActionDiscovery(testObj)
	require.NoError(t, err)
	assert.Empty(t, discoveryLua)
}

func TestGetResourceActionDiscoveryWithOverride(t *testing.T) {
	testObj := StrToUnstructured(objJSON)
	vm := VM{
		ResourceOverrides: map[string]appv1.ResourceOverride{
			"argoproj.io/Rollout": {
				Actions: string(grpc.MustMarshal(appv1.ResourceActions{
					ActionDiscoveryLua: validDiscoveryLua,
				})),
			},
		},
	}
	discoveryLua, err := vm.GetResourceActionDiscovery(testObj)
	require.NoError(t, err)
	assert.Equal(t, validDiscoveryLua, discoveryLua[0])
}

func TestGetResourceActionsWithBuiltInActionsFlag(t *testing.T) {
	testObj := StrToUnstructured(objJSON)
	vm := VM{
		ResourceOverrides: map[string]appv1.ResourceOverride{
			"argoproj.io/Rollout": {
				Actions: string(grpc.MustMarshal(appv1.ResourceActions{
					ActionDiscoveryLua:  validDiscoveryLua,
					MergeBuiltinActions: true,
				})),
			},
		},
	}

	discoveryLua, err := vm.GetResourceActionDiscovery(testObj)
	require.NoError(t, err)
	assert.Equal(t, validDiscoveryLua, discoveryLua[0])
}

const validDiscoveryLua = `
scaleParams = { {name = "replicas", type = "number"} }
scale = {name = 'scale', params = scaleParams}

resume = {name = 'resume'}

a = {scale = scale, resume = resume}

return a
`

const additionalValidDiscoveryLua = `
scaleParams = { {name = "override", type = "number"} }
scale = {name = 'scale', params = scaleParams}
prebuilt = {prebuilt = 'prebuilt', type = 'number'}

a = {scale = scale, prebuilt = prebuilt}

return a
`

func TestExecuteResourceActionDiscovery(t *testing.T) {
	testObj := StrToUnstructured(objJSON)
	vm := VM{}
	actions, err := vm.ExecuteResourceActionDiscovery(testObj, []string{validDiscoveryLua})
	require.NoError(t, err)
	expectedActions := []appv1.ResourceAction{
		{
			Name: "resume",
		}, {
			Name: "scale",
			Params: []appv1.ResourceActionParam{{
				Name: "replicas",
			}},
		},
	}
	for _, expectedAction := range expectedActions {
		assert.Contains(t, actions, expectedAction)
	}
}

func TestExecuteResourceActionDiscoveryWithDuplicationActions(t *testing.T) {
	testObj := StrToUnstructured(objJSON)
	vm := VM{}
	actions, err := vm.ExecuteResourceActionDiscovery(testObj, []string{validDiscoveryLua, additionalValidDiscoveryLua})
	require.NoError(t, err)
	expectedActions := []appv1.ResourceAction{
		{
			Name: "resume",
		},
		{
			Name: "scale",
			Params: []appv1.ResourceActionParam{{
				Name: "replicas",
			}},
		},
		{
			Name: "prebuilt",
		},
	}
	for _, expectedAction := range expectedActions {
		assert.Contains(t, actions, expectedAction)
	}
}

const discoveryLuaWithInvalidResourceAction = `
resume = {name = 'resume', invalidField: "test""}
a = {resume = resume}
return a`

func TestExecuteResourceActionDiscoveryInvalidResourceAction(t *testing.T) {
	testObj := StrToUnstructured(objJSON)
	vm := VM{}
	actions, err := vm.ExecuteResourceActionDiscovery(testObj, []string{discoveryLuaWithInvalidResourceAction})
	require.Error(t, err)
	assert.Nil(t, actions)
}

const invalidDiscoveryLua = `
a = 1
return a
`

func TestExecuteResourceActionDiscoveryInvalidReturn(t *testing.T) {
	testObj := StrToUnstructured(objJSON)
	vm := VM{}
	actions, err := vm.ExecuteResourceActionDiscovery(testObj, []string{invalidDiscoveryLua})
	assert.Nil(t, actions)
	require.Error(t, err)
}

const validActionLua = `
obj.metadata.labels["test"] = "test"
return obj
`

const expectedLuaUpdatedResult = `
apiVersion: argoproj.io/v1alpha1
kind: Rollout
metadata:
  labels:
    app.kubernetes.io/instance: helm-guestbook
    test: test
  name: helm-guestbook
  namespace: default
  resourceVersion: "123"
`

// Test an action that returns a single k8s resource json
func TestExecuteOldStyleResourceAction(t *testing.T) {
	testObj := StrToUnstructured(objJSON)
	expectedLuaUpdatedObj := StrToUnstructured(expectedLuaUpdatedResult)
	vm := VM{}
	newObjects, err := vm.ExecuteResourceAction(testObj, validActionLua, nil)
	require.NoError(t, err)
	assert.Len(t, newObjects, 1)
	assert.Equal(t, newObjects[0].K8SOperation, K8SOperation("patch"))
	assert.Equal(t, expectedLuaUpdatedObj, newObjects[0].UnstructuredObj)
}

const cronJobObjYaml = `
apiVersion: batch/v1
kind: CronJob
metadata:
  name: hello
  namespace: test-ns
`

const expectedCreatedJobObjList = `
- operation: create
  resource:
    apiVersion: batch/v1
    kind: Job
    metadata:
      name: hello-1
      namespace: test-ns
`

const expectedCreatedMultipleJobsObjList = `
- operation: create
  resource:
    apiVersion: batch/v1
    kind: Job
    metadata:
      name: hello-1
      namespace: test-ns
- operation: create
  resource:
    apiVersion: batch/v1
    kind: Job
    metadata:
      name: hello-2
      namespace: test-ns
`

const expectedActionMixedOperationObjList = `
- operation: create
  resource:
    apiVersion: batch/v1
    kind: Job
    metadata:
      name: hello-1
      namespace: test-ns
- operation: patch
  resource:
    apiVersion: batch/v1
    kind: CronJob
    metadata:
      name: hello
      namespace: test-ns
      labels:
        test: test
`

const createJobActionLua = `
job = {}
job.apiVersion = "batch/v1"
job.kind = "Job"

job.metadata = {}
job.metadata.name = "hello-1"
job.metadata.namespace = "test-ns"

impactedResource = {}
impactedResource.operation = "create"
impactedResource.resource = job
result = {}
result[1] = impactedResource

return result
`

const createMultipleJobsActionLua = `
job1 = {}
job1.apiVersion = "batch/v1"
job1.kind = "Job"

job1.metadata = {}
job1.metadata.name = "hello-1"
job1.metadata.namespace = "test-ns"

impactedResource1 = {}
impactedResource1.operation = "create"
impactedResource1.resource = job1
result = {}
result[1] = impactedResource1

job2 = {}
job2.apiVersion = "batch/v1"
job2.kind = "Job"

job2.metadata = {}
job2.metadata.name = "hello-2"
job2.metadata.namespace = "test-ns"

impactedResource2 = {}
impactedResource2.operation = "create"
impactedResource2.resource = job2

result[2] = impactedResource2

return result
`

const mixedOperationActionLuaOk = `
job1 = {}
job1.apiVersion = "batch/v1"
job1.kind = "Job"

job1.metadata = {}
job1.metadata.name = "hello-1"
job1.metadata.namespace = obj.metadata.namespace

impactedResource1 = {}
impactedResource1.operation = "create"
impactedResource1.resource = job1
result = {}
result[1] = impactedResource1

obj.metadata.labels = {}
obj.metadata.labels["test"] = "test"

impactedResource2 = {}
impactedResource2.operation = "patch"
impactedResource2.resource = obj

result[2] = impactedResource2

return result
`

const createMixedOperationActionLuaFailing = `
job1 = {}
job1.apiVersion = "batch/v1"
job1.kind = "Job"

job1.metadata = {}
job1.metadata.name = "hello-1"
job1.metadata.namespace = obj.metadata.namespace

impactedResource1 = {}
impactedResource1.operation = "create"
impactedResource1.resource = job1
result = {}
result[1] = impactedResource1

obj.metadata.labels = {}
obj.metadata.labels["test"] = "test"

impactedResource2 = {}
impactedResource2.operation = "thisShouldFail"
impactedResource2.resource = obj

result[2] = impactedResource2

return result
`

func TestExecuteNewStyleCreateActionSingleResource(t *testing.T) {
	testObj := StrToUnstructured(cronJobObjYaml)
	jsonBytes, err := yaml.YAMLToJSON([]byte(expectedCreatedJobObjList))
	require.NoError(t, err)
	t.Log(bytes.NewBuffer(jsonBytes).String())
	expectedObjects, err := UnmarshalToImpactedResources(bytes.NewBuffer(jsonBytes).String())
	require.NoError(t, err)
	vm := VM{}
	newObjects, err := vm.ExecuteResourceAction(testObj, createJobActionLua, nil)
	require.NoError(t, err)
	assert.Equal(t, expectedObjects, newObjects)
}

func TestExecuteNewStyleCreateActionMultipleResources(t *testing.T) {
	testObj := StrToUnstructured(cronJobObjYaml)
	jsonBytes, err := yaml.YAMLToJSON([]byte(expectedCreatedMultipleJobsObjList))
	require.NoError(t, err)
	// t.Log(bytes.NewBuffer(jsonBytes).String())
	expectedObjects, err := UnmarshalToImpactedResources(bytes.NewBuffer(jsonBytes).String())
	require.NoError(t, err)
	vm := VM{}
	newObjects, err := vm.ExecuteResourceAction(testObj, createMultipleJobsActionLua, nil)
	require.NoError(t, err)
	assert.Equal(t, expectedObjects, newObjects)
}

func TestExecuteNewStyleActionMixedOperationsOk(t *testing.T) {
	testObj := StrToUnstructured(cronJobObjYaml)
	jsonBytes, err := yaml.YAMLToJSON([]byte(expectedActionMixedOperationObjList))
	require.NoError(t, err)
	// t.Log(bytes.NewBuffer(jsonBytes).String())
	expectedObjects, err := UnmarshalToImpactedResources(bytes.NewBuffer(jsonBytes).String())
	require.NoError(t, err)
	vm := VM{}
	newObjects, err := vm.ExecuteResourceAction(testObj, mixedOperationActionLuaOk, nil)
	require.NoError(t, err)
	assert.Equal(t, expectedObjects, newObjects)
}

func TestExecuteNewStyleActionMixedOperationsFailure(t *testing.T) {
	testObj := StrToUnstructured(cronJobObjYaml)
	vm := VM{}
	_, err := vm.ExecuteResourceAction(testObj, createMixedOperationActionLuaFailing, nil)
	assert.ErrorContains(t, err, "unsupported operation")
}

func TestExecuteResourceActionNonTableReturn(t *testing.T) {
	testObj := StrToUnstructured(objJSON)
	vm := VM{}
	_, err := vm.ExecuteResourceAction(testObj, returnInt, nil)
	assert.Errorf(t, err, incorrectReturnType, "table", "number")
}

const invalidTableReturn = `newObj = {}
newObj["test"] = "test"
return newObj
`

func TestExecuteResourceActionInvalidUnstructured(t *testing.T) {
	testObj := StrToUnstructured(objJSON)
	vm := VM{}
	_, err := vm.ExecuteResourceAction(testObj, invalidTableReturn, nil)
	require.Error(t, err)
}

const objWithEmptyStruct = `
apiVersion: argoproj.io/v1alpha1
kind: Test
metadata:
  labels:
    app.kubernetes.io/instance: helm-guestbook
    test: test
  name: helm-guestbook
  namespace: default
  resourceVersion: "123"
spec:
  resources: {}
  paused: true
  containers:
   - name: name1
     test: {}
     anotherList:
     - name: name2
       test2: {}
`

const expectedUpdatedObjWithEmptyStruct = `
apiVersion: argoproj.io/v1alpha1
kind: Test
metadata:
  labels:
    app.kubernetes.io/instance: helm-guestbook
    test: test
  name: helm-guestbook
  namespace: default
  resourceVersion: "123"
spec:
  resources: {}
  paused: false
  containers:
   - name: name1
     test: {}
     anotherList:
     - name: name2
       test2: {}
`

const pausedToFalseLua = `
obj.spec.paused = false
return obj
`

func TestCleanPatch(t *testing.T) {
	testObj := StrToUnstructured(objWithEmptyStruct)
	expectedObj := StrToUnstructured(expectedUpdatedObjWithEmptyStruct)
	vm := VM{}
	newObjects, err := vm.ExecuteResourceAction(testObj, pausedToFalseLua, nil)
	require.NoError(t, err)
	assert.Len(t, newObjects, 1)
	assert.Equal(t, newObjects[0].K8SOperation, K8SOperation("patch"))
	assert.Equal(t, expectedObj, newObjects[0].UnstructuredObj)
}

func TestGetResourceHealth(t *testing.T) {
	const testSA = `
apiVersion: v1
kind: ServiceAccount
metadata:
  name: test
  namespace: test`

	const script = `
hs = {}
str = "Using lua standard library"
if string.find(str, "standard") then
  hs.message = "Standard lib was used"
else
  hs.message = "Standard lib was not used"
end
hs.status = "Healthy"
return hs`

	const healthWildcardOverrideScript = `
 hs = {}
 hs.status = "Healthy"
 return hs`

	const healthWildcardOverrideScriptUnhealthy = `
 hs = {}
 hs.status = "UnHealthy"
 return hs`

	getHealthOverride := func(openLibs bool) ResourceHealthOverrides {
		return ResourceHealthOverrides{
			"ServiceAccount": appv1.ResourceOverride{
				HealthLua:   script,
				UseOpenLibs: openLibs,
			},
		}
	}

	getWildcardHealthOverride := ResourceHealthOverrides{
		"*.aws.crossplane.io/*": appv1.ResourceOverride{
			HealthLua: healthWildcardOverrideScript,
		},
	}

	getMultipleWildcardHealthOverrides := ResourceHealthOverrides{
		"*.aws.crossplane.io/*": appv1.ResourceOverride{
			HealthLua: "",
		},
		"*.aws*": appv1.ResourceOverride{
			HealthLua: healthWildcardOverrideScriptUnhealthy,
		},
	}

	getBaseWildcardHealthOverrides := ResourceHealthOverrides{
		"*/*": appv1.ResourceOverride{
			HealthLua: "",
		},
	}

	t.Run("Enable Lua standard lib", func(t *testing.T) {
		testObj := StrToUnstructured(testSA)
		overrides := getHealthOverride(true)
		status, err := overrides.GetResourceHealth(testObj)
		require.NoError(t, err)
		expectedStatus := &health.HealthStatus{
			Status:  health.HealthStatusHealthy,
			Message: "Standard lib was used",
		}
		assert.Equal(t, expectedStatus, status)
	})

	t.Run("Disable Lua standard lib", func(t *testing.T) {
		testObj := StrToUnstructured(testSA)
		overrides := getHealthOverride(false)
		status, err := overrides.GetResourceHealth(testObj)
		assert.IsType(t, &lua.ApiError{}, err)
		expectedErr := "<string>:4: attempt to index a non-table object(nil) with key 'find'"
		require.EqualError(t, err, expectedErr)
		assert.Nil(t, status)
	})

	t.Run("Get resource health for wildcard override", func(t *testing.T) {
		testObj := StrToUnstructured(ec2AWSCrossplaneObjJSON)
		overrides := getWildcardHealthOverride
		status, err := overrides.GetResourceHealth(testObj)
		require.NoError(t, err)
		expectedStatus := &health.HealthStatus{
			Status: health.HealthStatusHealthy,
		}
		assert.Equal(t, expectedStatus, status)
	})

	t.Run("Get resource health for wildcard override with non-empty health.lua", func(t *testing.T) {
		testObj := StrToUnstructured(ec2AWSCrossplaneObjJSON)
		overrides := getMultipleWildcardHealthOverrides
		status, err := overrides.GetResourceHealth(testObj)
		require.NoError(t, err)
		expectedStatus := &health.HealthStatus{Status: "Unknown", Message: "Lua returned an invalid health status"}
		assert.Equal(t, expectedStatus, status)
	})

	t.Run("Get resource health for */* override with empty health.lua", func(t *testing.T) {
		testObj := StrToUnstructured(objWithNoScriptJSON)
		overrides := getBaseWildcardHealthOverrides
		status, err := overrides.GetResourceHealth(testObj)
		require.NoError(t, err)
		assert.Nil(t, status)
	})

	t.Run("Resource health for wildcard override not found", func(t *testing.T) {
		testObj := StrToUnstructured(testSA)
		overrides := getWildcardHealthOverride
		status, err := overrides.GetResourceHealth(testObj)
		require.NoError(t, err)
		assert.Nil(t, status)
	})
}

func TestExecuteResourceActionWithParams(t *testing.T) {
	deploymentObj := createMockResource("Deployment", "test-deployment", 1)
	statefulSetObj := createMockResource("StatefulSet", "test-statefulset", 1)

	actionLua := `
		obj.spec.replicas = tonumber(actionParams["replicas"])
		return obj
		`

	params := []*applicationpkg.ResourceActionParameters{
		{
			Name:  func() *string { s := "replicas"; return &s }(),
			Value: func() *string { s := "3"; return &s }(),
		},
	}

	vm := VM{}

	// Test with Deployment
	t.Run("Test with Deployment", func(t *testing.T) {
		impactedResources, err := vm.ExecuteResourceAction(deploymentObj, actionLua, params)
		require.NoError(t, err)

		for _, impactedResource := range impactedResources {
			modifiedObj := impactedResource.UnstructuredObj

			// Check the replicas in the modified object
			actualReplicas, found, err := unstructured.NestedInt64(modifiedObj.Object, "spec", "replicas")
			require.NoError(t, err)
			assert.True(t, found, "spec.replicas should be found in the modified object")
			assert.Equal(t, int64(3), actualReplicas, "replicas should be updated to 3")
		}
	})

	// Test with StatefulSet
	t.Run("Test with StatefulSet", func(t *testing.T) {
		impactedResources, err := vm.ExecuteResourceAction(statefulSetObj, actionLua, params)
		require.NoError(t, err)

		for _, impactedResource := range impactedResources {
			modifiedObj := impactedResource.UnstructuredObj

			// Check the replicas in the modified object
			actualReplicas, found, err := unstructured.NestedInt64(modifiedObj.Object, "spec", "replicas")
			require.NoError(t, err)
			assert.True(t, found, "spec.replicas should be found in the modified object")
			assert.Equal(t, int64(3), actualReplicas, "replicas should be updated to 3")
		}
	})
}

func createMockResource(kind string, name string, replicas int) *unstructured.Unstructured {
	return StrToUnstructured(fmt.Sprintf(`
    apiVersion: apps/v1
    kind: %s
    metadata:
      name: %s
      namespace: default
    spec:
      replicas: %d
      template:
        metadata:
          labels:
            app: test
        spec:
          containers:
          - name: test-container
            image: nginx
    `, kind, name, replicas))
}

func Test_getHealthScriptPaths(t *testing.T) {
	paths, err := getGlobHealthScriptPaths()
	require.NoError(t, err)

	// This test will fail any time a glob pattern is added to the health script paths. We don't expect that to happen
	// often.
	assert.Equal(t, []string{"_.crossplane.io/_", "_.upbound.io/_"}, paths)
}
