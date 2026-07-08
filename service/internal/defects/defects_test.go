package defects

import "testing"

func TestValidate(t *testing.T) {
	r := Report{}
	if err := r.Validate(); err == nil {
		t.Fatal("empty report must fail")
	}
	r.Reason = "AC looks wrong"
	if err := r.Validate(); err == nil {
		t.Fatal("missing creature_game_id must fail")
	}
	r.CreatureGameID = "abc123"
	if err := r.Validate(); err != nil {
		t.Fatalf("valid report rejected: %v", err)
	}
}
