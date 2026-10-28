package protocol

import (
	"bufio"
	"bytes"
	"net"
	"strconv"
	"strings"
	"testing"
)

func TestIMAPCursorStopsBeforeMissingBody(t *testing.T) {
	lines := []string{
		"* 1 FETCH (UID 10 BODY[] {4}",
		")",
		"* 3 FETCH (UID 12 BODY[] {6}",
		")",
		"A005 OK Fetch completed",
	}
	records := imapFetchRecords(lines, [][]byte{[]byte("ten!"), []byte("twelve")})
	if body := imapRecordBody(records, "12"); string(body) != "twelve" {
		t.Fatalf("UID 12 body = %q, want its own literal", body)
	}
	if body := imapRecordBody(records, "10"); string(body) != "ten!" {
		t.Fatalf("UID 10 body = %q", body)
	}
	cursor := imapCursorAfterFetch([]int{10, 11, 12}, records)
	if cursor != "10" {
		t.Fatalf("cursor = %q, want 10", cursor)
	}
}

func TestIMAPNilBodyDoesNotStealNextLiteral(t *testing.T) {
	lines := []string{
		"* 1 FETCH (UID 10 BODY[] NIL)",
		"* 2 FETCH (UID 11 BODY[] {6}",
		")",
		"A005 OK Fetch completed",
	}
	records := imapFetchRecords(lines, [][]byte{[]byte("otp-11")})
	if body := imapRecordBody(records, "11"); string(body) != "otp-11" {
		t.Fatalf("UID 11 body = %q, want otp-11", body)
	}
	if rec, ok := imapRecord(records, "10"); !ok || rec.Incomplete || len(rec.Raw) != 0 {
		t.Fatalf("NIL record = %+v ok=%v", rec, ok)
	}
	if cursor := imapCursorAfterFetch([]int{10, 11}, records); cursor != "11" {
		t.Fatalf("cursor = %q, want 11", cursor)
	}
}

func TestIMAPCursorStopsOnTruncatedBody(t *testing.T) {
	raw := bytes.Repeat([]byte("a"), imapPartialBodyLimit)
	lines := []string{
		"* 1 FETCH (UID 10 BODY[] {4}",
		")",
		"* 2 FETCH (UID 11 BODY[] {" + strconv.Itoa(imapPartialBodyLimit) + "}",
		")",
		"A005 OK Fetch completed",
	}
	records := imapFetchRecords(lines, [][]byte{[]byte("full"), raw})
	if rec, ok := imapRecord(records, "11"); !ok || !rec.Incomplete {
		t.Fatalf("truncated record = %+v ok=%v", rec, ok)
	}
	if cursor := imapCursorAfterFetch([]int{10, 11}, records); cursor != "10" {
		t.Fatalf("cursor = %q, want 10", cursor)
	}
}

func TestIMAPCursorAdvancesPastCompleteFilteredWindow(t *testing.T) {
	lines := []string{
		"* 1 FETCH (UID 8 BODY[] {3}",
		")",
		"* 2 FETCH (UID 9 BODY[] {3}",
		")",
		"A005 OK Fetch completed",
	}
	records := imapFetchRecords(lines, [][]byte{[]byte("aaa"), []byte("bbb")})
	if cursor := imapCursorAfterFetch(imapOldestWindow([]int{9, 8}, 50), records); cursor != "9" {
		t.Fatalf("cursor = %q, want 9", cursor)
	}
}

func TestIMAPCompletesTruncatedBodyBeforeCursor(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		reader := bufio.NewReader(server)
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		tag := strings.Fields(line)[0]
		body := []byte("OTP 654321")
		if _, err = server.Write([]byte("* 1 FETCH (UID 11 BODY[] {" + strconv.Itoa(len(body)) + "}\r\n")); err != nil {
			return
		}
		if _, err = server.Write(body); err != nil {
			return
		}
		_, _ = server.Write([]byte("\r\n)\r\n" + tag + " OK Fetch completed\r\n"))
	}()

	raw := bytes.Repeat([]byte("a"), imapPartialBodyLimit)
	tag := 5
	records, err := imapCompleteTruncated(client, bufio.NewReader(client), &tag, []iCloudIMAPFetchedMessage{{
		UID:        "11",
		Raw:        raw,
		Incomplete: true,
	}})
	<-done
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := imapRecord(records, "11")
	if !ok || rec.Incomplete || !bytes.HasSuffix(rec.Raw, []byte("OTP 654321")) {
		t.Fatalf("completed = %+v ok=%v", rec, ok)
	}
	if cursor := imapCursorAfterFetch([]int{11}, records); cursor != "11" {
		t.Fatalf("cursor = %q, want 11", cursor)
	}
}

func imapRecord(records []iCloudIMAPFetchedMessage, uid string) (iCloudIMAPFetchedMessage, bool) {
	for _, rec := range records {
		if rec.UID == uid {
			return rec, true
		}
	}
	return iCloudIMAPFetchedMessage{}, false
}

func imapRecordBody(records []iCloudIMAPFetchedMessage, uid string) []byte {
	rec, _ := imapRecord(records, uid)
	return rec.Raw
}
