package wasmhost

type ScContract struct {
	MapObject
}

func (o *ScContract) Exists(keyId int32) bool {
	switch keyId {
	case KeyAddress:
	case KeyColor:
	case KeyDescription:
	case KeyId:
	case KeyName:
	case KeyOwner:
	default:
		return false
	}
	return true
}

func (o *ScContract) GetBytes(keyId int32) []byte {
	switch keyId {
	case KeyAddress:
		return o.vm.ctx.GetSCAddress().Bytes()
	case KeyColor: //TODO
	case KeyId: //TODO
	case KeyOwner:
		return o.vm.ctx.GetOwnerAddress().Bytes()
	}
	return o.MapObject.GetBytes(keyId)
}

func (o *ScContract) GetString(keyId int32) string {
	switch keyId {
	case KeyDescription:
		return o.vm.GetDescription()
	case KeyName: //TODO
	}
	return o.MapObject.GetString(keyId)
}

func (o *ScContract) GetTypeId(keyId int32) int32 {
	switch keyId {
	case KeyAddress:
		return OBJTYPE_BYTES
	case KeyColor:
		return OBJTYPE_BYTES
	case KeyDescription:
		return OBJTYPE_STRING
	case KeyId:
		return OBJTYPE_STRING
	case KeyName:
		return OBJTYPE_STRING
	case KeyOwner:
		return OBJTYPE_BYTES
	}
	return -1
}
