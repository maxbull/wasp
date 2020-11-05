package consensus

import (
	"fmt"
	"github.com/iotaledger/goshimmer/dapps/valuetransfers/packages/address"
	"github.com/iotaledger/goshimmer/dapps/valuetransfers/packages/balance"
	valuetransaction "github.com/iotaledger/goshimmer/dapps/valuetransfers/packages/transaction"
	"github.com/iotaledger/wasp/packages/chain"
	"github.com/iotaledger/wasp/packages/coretypes"
	"github.com/iotaledger/wasp/packages/hashing"
	"github.com/iotaledger/wasp/packages/util"
	"github.com/iotaledger/wasp/packages/vm"
	"github.com/iotaledger/wasp/packages/vm/runvm"
)

type runCalculationsParams struct {
	requests        []*request
	leaderPeerIndex uint16
	balances        map[valuetransaction.ID][]*balance.Balance
	rewardAddress   address.Address
	timestamp       int64
}

// runs the VM for requests and posts result to committee's queue
func (op *operator) runCalculationsAsync(par runCalculationsParams) {
	if op.currentState == nil {
		op.log.Debugf("runCalculationsAsync: variable currentState is not known")
		return
	}
	var progHash hashing.HashValue
	if ph, ok := op.getProgramHash(); ok {
		// may not be needed if ready requests are only built-in
		progHash = *ph
	}
	ctx := &vm.VMTask{
		Processors:      op.chain.Processors(),
		LeaderPeerIndex: par.leaderPeerIndex,
		ProgramHash:     progHash,
		ChainID:         *op.chain.ID(),
		Color:           *op.chain.Color(),
		Entropy:         (hashing.HashValue)(op.stateTx.ID()),
		Balances:        par.balances,
		OwnerAddress:    *op.chain.OwnerAddress(),
		RewardAddress:   par.rewardAddress,
		MinimumReward:   op.getMinimumReward(),
		Requests:        takeRefs(par.requests),
		Timestamp:       par.timestamp,
		VirtualState:    op.currentState,
		Log:             op.log,
	}
	ctx.OnFinish = func(err error) {
		if err != nil {
			op.log.Errorf("VM task failed: %v", err)
			return
		}
		op.chain.ReceiveMessage(ctx)
	}
	if err := runvm.RunComputationsAsync(ctx); err != nil {
		op.log.Errorf("RunComputationsAsync: %v", err)
	}
}

func (op *operator) sendResultToTheLeader(result *vm.VMTask) {
	op.log.Debugw("sendResultToTheLeader")
	if op.consensusStage != consensusStageSubCalculationsStarted {
		op.log.Debugf("calculation result on SUB dismissed because stage changed from '%s' to '%s'",
			stages[consensusStageSubCalculationsStarted].name, stages[op.consensusStage].name)
		return
	}

	sigShare, err := op.dkshare.SignShare(result.ResultTransaction.EssenceBytes())
	if err != nil {
		op.log.Errorf("error while signing transaction %v", err)
		return
	}

	reqids := make([]coretypes.RequestID, len(result.Requests))
	for i := range reqids {
		reqids[i] = *result.Requests[i].RequestID()
	}

	essenceHash := hashing.HashData(result.ResultTransaction.EssenceBytes())
	batchHash := vm.BatchHash(reqids, result.Timestamp, result.LeaderPeerIndex)

	op.log.Debugw("sendResultToTheLeader",
		"leader", result.LeaderPeerIndex,
		"batchHash", batchHash.String(),
		"essenceHash", essenceHash.String(),
		"ts", result.Timestamp,
	)

	msgData := util.MustBytes(&chain.SignedHashMsg{
		PeerMsgHeader: chain.PeerMsgHeader{
			BlockIndex: op.mustStateIndex(),
		},
		BatchHash:     batchHash,
		OrigTimestamp: result.Timestamp,
		EssenceHash:   *essenceHash,
		SigShare:      sigShare,
	})

	if err := op.chain.SendMsg(result.LeaderPeerIndex, chain.MsgSignedHash, msgData); err != nil {
		op.log.Error(err)
		return
	}
	op.sentResultToLeader = result.ResultTransaction
	op.sentResultToLeaderIndex = result.LeaderPeerIndex

	op.setNextConsensusStage(consensusStageSubCalculationsFinished)
}

func (op *operator) saveOwnResult(result *vm.VMTask) {
	if op.consensusStage != consensusStageLeaderCalculationsStarted {
		op.log.Debugf("calculation result on LEADER dismissed because stage changed from '%s' to '%s'",
			stages[consensusStageLeaderCalculationsStarted].name, stages[op.consensusStage].name)
		return
	}
	sigShare, err := op.dkshare.SignShare(result.ResultTransaction.EssenceBytes())
	if err != nil {
		op.log.Errorf("error while signing transaction %v", err)
		return
	}

	reqids := make([]coretypes.RequestID, len(result.Requests))
	for i := range reqids {
		reqids[i] = *result.Requests[i].RequestID()
	}

	bh := vm.BatchHash(reqids, result.Timestamp, result.LeaderPeerIndex)
	if bh != op.leaderStatus.batchHash {
		panic("bh != op.leaderStatus.batchHash")
	}
	if len(result.Requests) != int(result.ResultBlock.Size()) {
		panic("len(result.RequestIDs) != int(result.ResultBlock.Size())")
	}

	essenceHash := hashing.HashData(result.ResultTransaction.EssenceBytes())
	op.log.Debugw("saveOwnResult",
		"batchHash", bh.String(),
		"ts", result.Timestamp,
		"essenceHash", essenceHash.String(),
	)

	op.leaderStatus.resultTx = result.ResultTransaction
	op.leaderStatus.batch = result.ResultBlock
	op.leaderStatus.signedResults[op.chain.OwnPeerIndex()] = &signedResult{
		essenceHash: *essenceHash,
		sigShare:    sigShare,
	}
	op.setNextConsensusStage(consensusStageLeaderCalculationsFinished)
}

func (op *operator) aggregateSigShares(sigShares [][]byte) error {
	resTx := op.leaderStatus.resultTx

	finalSignature, err := op.dkshare.RecoverFullSignature(sigShares, resTx.EssenceBytes())
	if err != nil {
		return err
	}
	if err := resTx.PutSignature(finalSignature); err != nil {
		return fmt.Errorf("something wrong while aggregating final signature: %v", err)
	}
	return nil
}