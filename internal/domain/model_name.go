package entity

import "strings"

// NormalizeModelName removes the gpt://folder_id/ prefix pattern from model names.
// For example: "gpt://b1g8t5pmnjifaov0paff/yandexgpt/rc" becomes "yandexgpt"
func NormalizeModelName(model string) string {
	normalizedModel := model
	if idx := strings.Index(normalizedModel, "gpt://"); idx >= 0 {
		remainder := normalizedModel[idx+6:] // skip "gpt://"
		if slashIdx := strings.Index(remainder, "/"); slashIdx >= 0 {
			normalizedModel = remainder[slashIdx+1:] // take everything after the folder ID
			// if there are more slashes (e.g. yandexgpt/rc), take only the first part
			if nextSlashIdx := strings.Index(normalizedModel, "/"); nextSlashIdx >= 0 {
				normalizedModel = normalizedModel[:nextSlashIdx]
			}
		}
	}
	return normalizedModel
}
