package mailbox

func deleteAPIKey(settingsErr error, key string) (string, error) {
	if settingsErr != nil {
		return "", settingsErr
	}
	return key, nil
}
