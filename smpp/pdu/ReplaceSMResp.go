package pdu

import (
	"github.com/sujit-baniya/protocol/smpp/data"
)

// ReplaceSMResp PDU.
type ReplaceSMResp struct {
	base
}

// NewReplaceSMResp returns ReplaceSMResp.
func NewReplaceSMResp() PDU {
	c := &ReplaceSMResp{
		base: newBase(),
	}
	c.CommandID = data.REPLACE_SM_RESP
	return c
}

// NewReplaceSMRespFromReq returns ReplaceSMResp.
func NewReplaceSMRespFromReq(req *ReplaceSM) PDU {
	c := NewReplaceSMResp().(*ReplaceSMResp)
	if req != nil {
		c.SequenceNumber = req.SequenceNumber
	}
	return c
}

// CanResponse implements PDU interface.
func (c *ReplaceSMResp) CanResponse() bool {
	return false
}

// GetResponse implements PDU interface.
func (c *ReplaceSMResp) GetResponse() PDU {
	return nil
}

// Marshal implements PDU interface.
func (c *ReplaceSMResp) Marshal(b *ByteBuffer) {
	c.base.marshal(b, nil)
}

// Unmarshal implements PDU interface.
func (c *ReplaceSMResp) Unmarshal(b *ByteBuffer) error {
	return c.base.unmarshal(b, nil)
}
