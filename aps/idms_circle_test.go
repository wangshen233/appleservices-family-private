package aps

import "testing"

func TestCirclePayloadIsPreserved(t *testing.T) {
	event, err := ParseEvent([]byte(`{"cmd":700,"adsid":"test","txnid":"signin","atxnid":"circle","step":1,"pake":"DER","idmsdata":"opaque","ec":0,"aps":{"alert":{"title":"test"}}}`))
	if err != nil || event.CircleTransactionID != "circle" || event.CircleStep != 1 || event.PAKE != "DER" || event.IDMSData != "opaque" || event.ErrorCode == nil || *event.ErrorCode != 0 {
		t.Fatal("Circle protocol metadata lost")
	}
}
