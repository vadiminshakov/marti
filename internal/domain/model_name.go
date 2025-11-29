package domain

import "strings"

// normalizeModelName removes the gpt://folder_id/ prefix pattern.
func normalizeModelName(model string) string {
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
