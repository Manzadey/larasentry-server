package github

import (
	"strings"

	"server/internal/domain"
)

// ExtractSnippet разбивает содержимое файла на строки и возвращает окно строк кода
// вокруг targetLine с заданным радиусом.
func ExtractSnippet(rawContent string, targetLine int, radius int) []domain.CodeLine {
	if rawContent == "" || targetLine <= 0 {
		return nil
	}

	normalized := strings.ReplaceAll(rawContent, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	allLines := strings.Split(normalized, "\n")
	totalLines := len(allLines)
	if totalLines == 0 {
		return nil
	}

	if radius <= 0 {
		radius = 5
	}

	start := targetLine - radius
	if start < 1 {
		start = 1
	}

	end := targetLine + radius
	if end > totalLines {
		end = totalLines
	}

	if start > totalLines {
		return nil
	}

	result := make([]domain.CodeLine, 0, end-start+1)
	for i := start; i <= end; i++ {
		lineIdx := i - 1
		result = append(result, domain.CodeLine{
			Number:  i,
			Code:    allLines[lineIdx],
			IsError: (i == targetLine),
		})
	}

	return result
}
