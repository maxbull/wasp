package trclient

import (
	"bytes"
	"sort"

	"github.com/iotaledger/goshimmer/dapps/valuetransfers/packages/address"
	"github.com/iotaledger/goshimmer/dapps/valuetransfers/packages/balance"
	"github.com/iotaledger/wasp/client/scclient"
	"github.com/iotaledger/wasp/client/statequery"
	"github.com/iotaledger/wasp/packages/sctransaction"
	"github.com/iotaledger/wasp/packages/vm/examples/tokenregistry"
)

type TokenRegistryClient struct {
	*scclient.SCClient
}

func NewClient(scClient *scclient.SCClient) *TokenRegistryClient {
	return &TokenRegistryClient{scClient}
}

type MintAndRegisterParams struct {
	Supply          int64           // number of tokens to mint
	MintTarget      address.Address // where to mint new Supply
	Description     string
	UserDefinedData []byte
}

func (trc *TokenRegistryClient) OwnerAddress() address.Address {
	return trc.SigScheme.Address()
}

// MintAndRegister mints new Supply of colored tokens to some address and sends request
// to register it in the TokenRegistry smart contract
func (trc *TokenRegistryClient) MintAndRegister(par MintAndRegisterParams) (*sctransaction.Transaction, error) {
	args := make(map[string]interface{})
	args[tokenregistry.VarReqDescription] = par.Description
	if par.UserDefinedData != nil {
		args[tokenregistry.VarReqUserDefinedMetadata] = par.UserDefinedData
	}
	return trc.PostRequest(
		tokenregistry.RequestMintSupply,
		map[address.Address]int64{par.MintTarget: par.Supply},
		nil,
		args,
	)
}

type Status struct {
	*scclient.SCStatus

	Registry                     map[balance.Color]*tokenregistry.TokenMetadata
	RegistrySortedByMintTimeDesc []*TokenMetadataWithColor // may be nil
}

type TokenMetadataWithColor struct {
	tokenregistry.TokenMetadata
	Color balance.Color
}

func (trc *TokenRegistryClient) FetchStatus(sortByAgeDesc bool) (*Status, error) {
	scStatus, results, err := trc.FetchSCStatus(func(query *statequery.Request) {
		query.AddDictionary(tokenregistry.VarStateTheRegistry, 100)
	})
	if err != nil {
		return nil, err
	}

	status := &Status{SCStatus: scStatus}

	status.Registry, err = decodeRegistry(results.Get(tokenregistry.VarStateTheRegistry).MustDictionaryResult())
	if err != nil {
		return nil, err
	}

	if !sortByAgeDesc {
		return status, nil
	}
	tslice := make([]*TokenMetadataWithColor, 0, len(status.Registry))
	for col, ti := range status.Registry {
		tslice = append(tslice, &TokenMetadataWithColor{
			TokenMetadata: *ti,
			Color:         col,
		})
	}
	sort.Slice(tslice, func(i, j int) bool {
		return tslice[i].Created > tslice[j].Created
	})
	status.RegistrySortedByMintTimeDesc = tslice
	return status, nil
}

func decodeRegistry(result *statequery.DictResult) (map[balance.Color]*tokenregistry.TokenMetadata, error) {
	registry := make(map[balance.Color]*tokenregistry.TokenMetadata)
	for _, e := range result.Entries {
		color, _, err := balance.ColorFromBytes(e.Key)
		if err != nil {
			return nil, err
		}
		tm := &tokenregistry.TokenMetadata{}
		if err := tm.Read(bytes.NewReader(e.Value)); err != nil {
			return nil, err
		}
		registry[color] = tm
	}
	return registry, nil
}

func (trc *TokenRegistryClient) Query(color *balance.Color) (*tokenregistry.TokenMetadata, error) {
	query := statequery.NewRequest()
	query.AddDictionaryElement(tokenregistry.VarStateTheRegistry, color.Bytes())

	res, err := trc.StateQuery(query)
	if err != nil {
		return nil, err
	}

	value := res.Get(tokenregistry.VarStateTheRegistry).MustDictionaryElementResult()
	if value == nil {
		// not found
		return nil, nil
	}

	tm := &tokenregistry.TokenMetadata{}
	if err := tm.Read(bytes.NewReader(value)); err != nil {
		return nil, err
	}

	return tm, nil
}
