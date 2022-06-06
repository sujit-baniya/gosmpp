package pdu

import (
	"github.com/sujit-baniya/protocol/smpp/coding"
	"testing"

	"github.com/sujit-baniya/protocol/smpp/data"

	"github.com/stretchr/testify/require"
)

func TestDeliverSM(t *testing.T) {
	v := NewDeliverSM().(*DeliverSM)
	require.True(t, v.CanResponse())
	v.SequenceNumber = 13

	validate(t,
		v.GetResponse(),
		"0000001180000005000000000000000d00",
		data.DELIVER_SM_RESP,
	)

	v.ServiceType = "abc"
	_ = v.SourceAddr.SetAddress("Alicer")
	v.SourceAddr.SetTon(28)
	v.SourceAddr.SetNpi(29)
	_ = v.DestAddr.SetAddress("Bobo")
	v.DestAddr.SetTon(30)
	v.DestAddr.SetNpi(31)
	v.EsmClass = 77
	v.ProtocolID = 99
	v.PriorityFlag = 61
	v.RegisteredDelivery = 83
	_ = v.Message.SetMessageWithEncoding("nghắ nghiêng nghiễng ngả", coding.UCS2)
	v.Message.message = ""

	validate(t,
		v,
		"0000005e00000005000000000000000d616263001c1d416c69636572001e1f426f626f004d633d00005300080030006e006700681eaf0020006e00670068006900ea006e00670020006e0067006800691ec5006e00670020006e00671ea3",
		data.DELIVER_SM,
	)
}
