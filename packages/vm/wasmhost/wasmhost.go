package wasmhost

import (
	"encoding/binary"
	"fmt"
	"github.com/mr-tron/base58"
)

const (
	OBJTYPE_BYTES        int32 = 0
	OBJTYPE_BYTES_ARRAY  int32 = 1
	OBJTYPE_INT          int32 = 2
	OBJTYPE_INT_ARRAY    int32 = 3
	OBJTYPE_MAP          int32 = 4
	OBJTYPE_MAP_ARRAY    int32 = 5
	OBJTYPE_STRING       int32 = 6
	OBJTYPE_STRING_ARRAY int32 = 7
)

const (
	KeyError       = int32(-1)
	KeyLength      = KeyError - 1
	KeyLog         = KeyLength - 1
	KeyTrace       = KeyLog - 1
	KeyTraceHost   = KeyTrace - 1
	KeyWarning     = KeyTraceHost - 1
	KeyUserDefined = KeyWarning - 1
)

type HostObject interface {
	Exists(keyId int32) bool
	GetBytes(keyId int32) []byte
	GetInt(keyId int32) int64
	GetObjectId(keyId int32, typeId int32) int32
	GetString(keyId int32) string
	GetTypeId(keyId int32) int32
	SetBytes(keyId int32, value []byte)
	SetInt(keyId int32, value int64)
	SetString(keyId int32, value string)
}

type LogInterface interface {
	Log(logLevel int32, text string)
}

var baseKeyMap = map[string]int32{
	"error":     KeyError,
	"length":    KeyLength,
	"log":       KeyLog,
	"trace":     KeyTrace,
	"traceHost": KeyTraceHost,
	"warning":   KeyWarning,
}

type WasmVM interface {
	LinkHost(host *WasmHost) error
	LoadWasm(wasmData []byte) error
	RunFunction(functionName string) error
	UnsafeMemory() []byte
}

type WasmHost struct {
	vm            WasmVM
	codeToFunc    map[int32]string
	error         string
	funcToCode    map[string]int32
	keyIdToKey    [][]byte
	keyIdToKeyMap [][]byte
	keyMapToKeyId *map[string]int32
	keyToKeyId    map[string]int32
	logger        LogInterface
	memoryCopy    []byte
	memoryDirty   bool
	memoryNonZero int
	objIdToObj    []HostObject
	useBase58Keys bool
}

func (host *WasmHost) Init(null HostObject, root HostObject, keyMap *map[string]int32, logger LogInterface) error {
	if keyMap == nil {
		keyMap = &baseKeyMap
	}
	elements := len(*keyMap) + 1
	host.codeToFunc = make(map[int32]string)
	host.error = ""
	host.funcToCode = make(map[string]int32)
	host.logger = logger
	host.objIdToObj = nil
	host.keyIdToKey = [][]byte{[]byte("<null>")}
	host.keyMapToKeyId = keyMap
	host.keyToKeyId = make(map[string]int32)
	host.keyIdToKeyMap = make([][]byte, elements)
	for k, v := range *keyMap {
		host.keyIdToKeyMap[-v] = []byte(k)
	}
	host.TrackObject(null)
	host.TrackObject(root)
	host.vm = NewWasmTimeVM()
	return host.vm.LinkHost(host)
}

func (host *WasmHost) CallFunction(functionName string) error {
	//TODO what about passing args and results?
	ptr := host.vm.UnsafeMemory()
	saved := make([]byte, len(ptr))
	copy(saved, ptr)
	err := host.RunFunction(functionName)
	copy(ptr, saved)
	return err
}

func (host *WasmHost) fdWrite(fd int32, iovs int32, size int32, written int32) int32 {
	// very basic implementation that expects fd to be stdout and iovs to be only one element
	ptr := host.vm.UnsafeMemory()
	txt := binary.LittleEndian.Uint32(ptr[iovs : iovs+4])
	siz := binary.LittleEndian.Uint32(ptr[iovs+4 : iovs+8])
	fmt.Print(string(ptr[txt : txt+siz]))
	binary.LittleEndian.PutUint32(ptr[written:written+4], siz)
	return int32(siz)
}

func (host *WasmHost) FindObject(objId int32) HostObject {
	if objId < 0 || objId >= int32(len(host.objIdToObj)) {
		host.SetError("Invalid objId")
		objId = 0
	}
	return host.objIdToObj[objId]
}

func (host *WasmHost) GetBytes(objId int32, keyId int32, stringRef int32, size int32) int32 {
	// get error string takes precedence over returning error code
	if keyId == KeyError && objId == -1 {
		host.Trace("GetString o%d k%d = '%s'", -objId, keyId, host.error)
		return host.vmSetBytes(stringRef, size, []byte(host.error))
	}

	if host.HasError() {
		return -1
	}

	if objId < 0 {
		// negative objId means get string
		obj := host.FindObject(-objId)
		if !obj.Exists(keyId) {
			host.Trace("GetString o%d k%d missing key", -objId, keyId)
			return -1
		}

		value := obj.GetString(keyId)
		host.Trace("GetString o%d k%d = '%s'", -objId, keyId, value)
		return host.vmSetBytes(stringRef, size, []byte(value))
	}

	// non-negative objId means get bytes
	obj := host.FindObject(objId)
	if !obj.Exists(keyId) {
		host.Trace("GetBytes o%d k%d missing key", objId, keyId)
		return -1
	}
	value := obj.GetBytes(keyId)
	host.Trace("GetBytes o%d k%d = '%s'", objId, keyId, base58.Encode(value))
	return host.vmSetBytes(stringRef, size, value)
}

func (host *WasmHost) GetInt(objId int32, keyId int32) int64 {
	if keyId == KeyError && objId == 1 {
		if host.HasError() {
			return 1
		}
		return 0
	}
	if host.HasError() {
		return 0
	}
	value := host.FindObject(objId).GetInt(keyId)
	host.Trace("GetInt o%d k%d = %d", objId, keyId, value)
	return value
}

func (host *WasmHost) GetKey(keyId int32) []byte {
	key := host.getKey(keyId)
	if key[len(key)-1] != 0 {
		// originally a string key
		host.Trace("GetKey k%d='%s'", keyId, string(key))
		return key
	}
	// originally a byte slice key
	host.Trace("GetKey k%d='%s'", keyId, base58.Encode(key))
	return key
}

func (host *WasmHost) getKey(keyId int32) []byte {
	// find predefined key
	if keyId < 0 {
		return host.keyIdToKeyMap[-keyId]
	}

	// find user-defined key
	if keyId < int32(len(host.keyIdToKey)) {
		return host.keyIdToKey[keyId]
	}

	// unknown key
	return nil
}

func (host *WasmHost) GetKeyId(keyRef int32, size int32) int32 {
	// non-negative size means original key was a string
	if size >= 0 {
		key := host.vmGetBytes(keyRef, size)
		keyId := host.GetKeyIdFromBytes(key)
		host.Trace("GetKeyId '%s'=k%d", string(key), keyId)
		return keyId
	}

	// negative size means original key was a byte slice
	key := host.vmGetBytes(keyRef, -size-1)

	if !host.useBase58Keys {
		// use byte slice key as is
		keyId := host.GetKeyIdFromBytes(key)
		host.Trace("GetKeyId '%s'=k%d", base58.Encode(key), keyId)
		return keyId
	}

	// transform byte slice key into base58 string
	// now all keys are byte slices from strings
	base58Key := base58.Encode(key)
	key = []byte(base58Key)
	keyId := host.GetKeyIdFromBytes(key)
	host.Trace("GetKeyId '%s'=k%d", base58Key, keyId)
	return keyId
}

func (host *WasmHost) GetKeyIdFromBytes(key []byte) int32 {
	// cannot use []byte as key in maps
	// so we will convert to (non-utf8) string
	// most will have started out as string anyway
	keyString := string(key)

	// first check predefined key map
	keyId, ok := (*host.keyMapToKeyId)[keyString]
	if ok {
		return keyId
	}

	// check additional user-defined keys
	keyId, ok = host.keyToKeyId[keyString]
	if ok {
		return keyId
	}

	// unknown key, add it to user-defined key map
	keyId = int32(len(host.keyIdToKey))
	host.keyToKeyId[keyString] = keyId
	host.keyIdToKey = append(host.keyIdToKey, key)
	return keyId
}

func (host *WasmHost) GetObjectId(objId int32, keyId int32, typeId int32) int32 {
	if host.HasError() {
		return 0
	}
	subId := host.FindObject(objId).GetObjectId(keyId, typeId)
	host.Trace("GetObjectId o%d k%d t%d = o%d", objId, keyId, typeId, subId)
	return subId
}

func (host *WasmHost) HasError() bool {
	if host.error != "" {
		host.Trace("HasError")
		return true
	}
	return false
}

func (host *WasmHost) LoadWasm(wasmData []byte) error {
	err := host.vm.LoadWasm(wasmData)
	if err != nil {
		return err
	}

	// find initialized data range in memory
	ptr := host.vm.UnsafeMemory()
	firstNonZero := 0
	lastNonZero := 0
	for i, b := range ptr {
		if b != 0 {
			if firstNonZero == 0 {
				firstNonZero = i
			}
			lastNonZero = i
		}
	}

	// save copy of initialized data range
	host.memoryNonZero = len(ptr)
	if ptr[firstNonZero] != 0 {
		host.memoryNonZero = firstNonZero
		size := lastNonZero + 1 - firstNonZero
		host.memoryCopy = make([]byte, size)
		copy(host.memoryCopy, ptr[host.memoryNonZero:])
	}
	return nil
}

func (host *WasmHost) RunFunction(functionName string) error {
	if host.memoryDirty {
		// clear memory and restore initialized data range
		ptr := host.vm.UnsafeMemory()
		copy(ptr, make([]byte, len(ptr)))
		copy(ptr[host.memoryNonZero:], host.memoryCopy)
	}
	host.memoryDirty = true
	return host.vm.RunFunction(functionName)
}

func (host *WasmHost) SetBytes(objId int32, keyId int32, stringRef int32, size int32) {
	bytes := host.vmGetBytes(stringRef, size)
	if objId == -1 {
		// intercept logging keys to prevent final logging of SetBytes itself
		switch keyId {
		case KeyError:
			host.SetError(string(bytes))
			return
		case KeyLog, KeyTrace, KeyTraceHost:
			host.logger.Log(keyId, string(bytes))
			return
		}
	}

	if host.HasError() {
		return
	}

	if objId < 0 {
		value := string(bytes)
		host.FindObject(-objId).SetString(keyId, value)
		host.Trace("SetString o%d k%d v='%s'", -objId, keyId, value)
		return
	}

	host.FindObject(objId).SetBytes(keyId, bytes)
	host.Trace("SetBytes o%d k%d v='%s'", objId, keyId, base58.Encode(bytes))
}

func (host *WasmHost) SetError(text string) {
	host.Trace("SetError '%s'", text)
	if !host.HasError() {
		host.error = text
	}
}

func (host *WasmHost) SetExport(keyId int32, value string) {
	_, ok := host.codeToFunc[keyId]
	if ok {
		host.SetError("SetExport: duplicate code")
	}
	_, ok = host.funcToCode[value]
	if ok {
		host.SetError("SetExport: duplicate function")
	}
	host.funcToCode[value] = keyId
	host.codeToFunc[keyId] = value
}

func (host *WasmHost) SetInt(objId int32, keyId int32, value int64) {
	if host.HasError() {
		return
	}
	host.FindObject(objId).SetInt(keyId, value)
	host.Trace("SetInt o%d k%d v=%d", objId, keyId, value)
}

func (host *WasmHost) Trace(format string, a ...interface{}) {
	host.logger.Log(KeyTrace, fmt.Sprintf(format, a...))
}

func (host *WasmHost) TrackObject(obj HostObject) int32 {
	objId := int32(len(host.objIdToObj))
	host.objIdToObj = append(host.objIdToObj, obj)
	return objId
}

func (host *WasmHost) vmGetBytes(offset int32, size int32) []byte {
	ptr := host.vm.UnsafeMemory()
	bytes := make([]byte, size)
	copy(bytes, ptr[offset:offset+size])
	return bytes
}

func (host *WasmHost) vmGetInt(offset int32) int64 {
	ptr := host.vm.UnsafeMemory()
	return int64(binary.LittleEndian.Uint64(ptr[offset : offset+8]))
}

func (host *WasmHost) vmSetBytes(offset int32, size int32, bytes []byte) int32 {
	if size != 0 {
		ptr := host.vm.UnsafeMemory()
		copy(ptr[offset:offset+size], bytes)
	}
	return int32(len(bytes))
}

func (host *WasmHost) vmSetInt(offset int32, value int64) {
	ptr := host.vm.UnsafeMemory()
	binary.LittleEndian.PutUint64(ptr[offset:offset+8], uint64(value))
}
