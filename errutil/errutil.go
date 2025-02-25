package errutil

import "uptime-service/logging"

func HandleError(context string, err error) bool {
	if err != nil {
		logging.Errorf("%s: %v", context, err)
		return true
	}
	return false
}
