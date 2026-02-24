package whatsapp

import "testing"

func TestWhatsAppLogState_RecordAndConsumeEncryptionFailure(t *testing.T) {
	state := &whatsAppLogState{
		encryptFailures: make(map[string]encryptFailure),
	}

	state.recordEncryptionFailure("Failed to encrypt 3EB01C086C301793D0A833 for 919500080653@s.whatsapp.net: can't encrypt message for device: no signal session established")
	state.recordEncryptionFailure("Failed to encrypt 3EB01C086C301793D0A833 for 919500080653:5@s.whatsapp.net: can't encrypt message for device: no signal session established")

	count, last := state.consumeEncryptionFailure("3EB01C086C301793D0A833")
	if count != 2 {
		t.Fatalf("expected count=2, got %d", count)
	}
	if last == "" {
		t.Fatal("expected last error line to be populated")
	}

	count, last = state.consumeEncryptionFailure("3EB01C086C301793D0A833")
	if count != 0 || last != "" {
		t.Fatalf("expected consumed failure to be removed, got count=%d last=%q", count, last)
	}
}
