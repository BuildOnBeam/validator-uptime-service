package contract

import (
	"testing"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/libevm/common"
	"github.com/ava-labs/libevm/core/types"
)

const testStakingManager = "0x2FD428A5484d113294b44E69Cb9f269abC1d5B54"

func TestUptimeUpdatedTopic(t *testing.T) {
	want := common.HexToHash("0x91f0d6ad8feb98e052c01a7e9a96db840e7ea2aba1df8e5683bdb619be6e8a2a")
	if uptimeUpdatedTopic != want {
		t.Fatalf("topic = %s, want %s", uptimeUpdatedTopic, want)
	}
}

func TestRecordedUptime(t *testing.T) {
	c := ContractClient{StakingManagerAddress: testStakingManager}
	validationID := ids.ID(common.HexToHash("0xead34b8c96803a2be8f783fe6496e5163ebcf24f530b9626878c4130e339b277"))
	otherID := ids.ID(common.HexToHash("0xc74ff84506a0568be2d90671fd54d1af20aa2d24ce90d13bf482249aec50f03d"))

	// Log emitted by a recorded submission: uptime 48968016 in epoch 682.
	recordedLog := &types.Log{
		Address: common.HexToAddress(testStakingManager),
		Topics:  []common.Hash{uptimeUpdatedTopic, common.Hash(validationID)},
		Data: common.FromHex("0x0000000000000000000000000000000000000000000000000000000002eb3150" +
			"00000000000000000000000000000000000000000000000000000000000002aa"),
	}

	tests := []struct {
		name    string
		logs    []*types.Log
		id      ids.ID
		want    uint64
		wantRec bool
	}{
		{name: "recorded", logs: []*types.Log{recordedLog}, id: validationID, want: 48_968_016, wantRec: true},
		{name: "successful tx without event", logs: nil, id: validationID},
		{name: "event for another validation", logs: []*types.Log{recordedLog}, id: otherID},
		{
			name: "event from another contract",
			logs: []*types.Log{{
				Address: common.HexToAddress("0x0000000000000000000000000000000000000001"),
				Topics:  recordedLog.Topics,
				Data:    recordedLog.Data,
			}},
			id: validationID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := c.recordedUptime(&types.Receipt{Status: types.ReceiptStatusSuccessful, Logs: tt.logs}, tt.id)
			if ok != tt.wantRec || got != tt.want {
				t.Fatalf("recordedUptime = (%d, %v), want (%d, %v)", got, ok, tt.want, tt.wantRec)
			}
		})
	}
}
