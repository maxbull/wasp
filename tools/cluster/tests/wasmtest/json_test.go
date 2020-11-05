package wasmtest

import (
	"encoding/json"
	"github.com/iotaledger/goshimmer/dapps/valuetransfers/packages/balance"
	"github.com/iotaledger/wasp/packages/kv"
	"github.com/iotaledger/wasp/packages/testutil"
	"github.com/iotaledger/wasp/packages/vm/examples/inccounter"
	"github.com/iotaledger/wasp/packages/vm/vmconst"
	"github.com/iotaledger/wasp/packages/vm/wasmhost"
	"os"
	"testing"
)

func TestOne(t *testing.T) {
	wasps := setup(t, "TestOne")

	jsonTests := loadJson("tests/increment.json")
	contract := jsonTests.Setups["default"].Contract
	wasmName := contract["name"].(string)
	description := contract["description"].(string)
	err := loadWasmIntoWasps(wasps, "wasm/"+wasmName, description)
	check(err, t)

	err = requestFunds(wasps, scOwnerAddr, "sc owner")
	check(err, t)

	scAddr, scColor, err := startSmartContract(wasps, inccounter.ProgramHash, description)
	checkSuccess(err, t, "smart contract has been created and activated")

	if !wasps.VerifyAddressBalances(scOwnerAddr, testutil.RequestFundsAmount-1, map[balance.Color]int64{
		balance.ColorIOTA: testutil.RequestFundsAmount - 1,
	}, "sc owner in the end") {
		t.Fail()
		return
	}

	if !wasps.VerifyAddressBalances(scAddr, 1, map[balance.Color]int64{
		*scColor: 1,
	}, "sc in the end") {
		t.Fail()
		return
	}

	if !wasps.VerifySCStateVariables2(scAddr, map[kv.Key]interface{}{
		vmconst.VarNameOwnerAddress: scOwnerAddr[:],
		vmconst.VarNameProgramHash:  programHash[:],
		vmconst.VarNameDescription:  description,
	}) {
		t.Fail()
	}
}

func loadJson(name string) *wasmhost.JsonTests {
	file, err := os.Open(name)
	if err != nil {
		panic(err)
	}
	defer file.Close()
	jsonTests := &wasmhost.JsonTests{}
	err = json.NewDecoder(file).Decode(&jsonTests)
	if err != nil {
		panic("JSON error: " + err.Error())
	}
	return jsonTests
}
