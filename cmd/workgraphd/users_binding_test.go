package main

import "testing"

func TestHostedUserCreationChecksBindingIdentityAndPreservesThePublicReceipt(t *testing.T) {
	const user = "11111111-1111-4111-8111-111111111111"
	const subject = "22222222-2222-4222-8222-222222222222"
	receipt, err := decodeUserBinding([]byte(`{"status":"created","user_id":"`+user+`","subject":"`+subject+`"}`), subject, "engineer")
	if err != nil || receipt.UserID != user || receipt.Subject != subject || receipt.Username != "engineer" || receipt.Status != "created" {
		t.Fatal("host create receipt differs from direct create", err)
	}
	for _, raw := range []string{
		`{"status":"created","user_id":"` + user + `","subject":"wrong"}`,
		`{"status":"created","user_id":"invalid","subject":"` + subject + `"}`,
		`{"status":"created","user_id":"` + user + `","subject":"` + subject + `","token":"unexpected"}`,
	} {
		if _, err := decodeUserBinding([]byte(raw), subject, "engineer"); err == nil {
			t.Fatal("uncertain/wrong binding acknowledged")
		}
	}
}
