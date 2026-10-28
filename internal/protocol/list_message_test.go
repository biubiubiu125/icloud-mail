package protocol

import (
	"strings"
	"testing"
)

func TestDuplicateListMessageHidesMailboxAddress(t *testing.T) {
	err := duplicateRemoteListError("icloud_mailbox_list_incomplete", "id", "")
	message := PublicMessage(err)
	if strings.Contains(message, "@") || strings.Contains(message, "alias@icloud.com") {
		t.Fatalf("message leaked an address: %s", message)
	}
	if !strings.Contains(message, "未执行本地远端缺失标记") {
		t.Fatalf("message = %s", message)
	}
	apple := PublicMessage(duplicateRemoteListError("apple_account_mailbox_list_incomplete", "email", "阶段：读取隐私邮箱列表；HTTP 200"))
	if strings.Contains(apple, "@") {
		t.Fatalf("apple message leaked an address: %s", apple)
	}
}
