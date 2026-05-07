package mission

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

type reviewAnswer struct {
	Findings               []reviewFinding `json:"findings"`
	OverallCorrectness     string          `json:"overall_correctness"`
	OverallExplanation     string          `json:"overall_explanation"`
	OverallConfidenceScore float64         `json:"overall_confidence_score"`
}

type reviewFinding struct {
	Title           string             `json:"title"`
	Body            string             `json:"body"`
	ConfidenceScore float64            `json:"confidence_score"`
	Priority        *int               `json:"priority"`
	CodeLocation    reviewCodeLocation `json:"code_location"`
}

type reviewCodeLocation struct {
	AbsoluteFilePath string          `json:"absolute_file_path"`
	LineRange        reviewLineRange `json:"line_range"`
}

type reviewLineRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type reviewDisplayLine struct {
	Text string
	Tone string
}

func parseReviewAnswer(text string) (reviewAnswer, bool) {
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "FINAL ANSWER:")
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "{") {
		return reviewAnswer{}, false
	}
	var answer reviewAnswer
	if err := json.Unmarshal([]byte(text), &answer); err != nil {
		return reviewAnswer{}, false
	}
	if len(answer.Findings) == 0 && answer.OverallCorrectness == "" && answer.OverallExplanation == "" {
		return reviewAnswer{}, false
	}
	return answer, true
}

func reviewAnswerLines(text string) ([]string, bool) {
	display, ok := reviewAnswerDisplayLines(text)
	if !ok {
		return nil, false
	}
	lines := make([]string, 0, len(display))
	for _, line := range display {
		lines = append(lines, line.Text)
	}
	return lines, true
}

func reviewAnswerDisplayLines(text string) ([]reviewDisplayLine, bool) {
	answer, ok := parseReviewAnswer(text)
	if !ok {
		return nil, false
	}

	status := fallback(strings.TrimSpace(answer.OverallCorrectness), "review complete")
	header := fmt.Sprintf("REVIEW REPORT: %s", status)
	if len(answer.Findings) > 0 {
		header += fmt.Sprintf("  findings %d", len(answer.Findings))
	}
	if answer.OverallConfidenceScore > 0 {
		header += fmt.Sprintf("  confidence %.2f", answer.OverallConfidenceScore)
	}

	lines := []reviewDisplayLine{{Text: header, Tone: "review-header"}}
	for _, finding := range answer.Findings {
		lines = append(lines, reviewDisplayLine{
			Text: reviewFindingHeadline(finding),
			Tone: reviewFindingTone(finding),
		})
		body := oneLine(finding.Body)
		if body != "" {
			lines = append(lines, reviewDisplayLine{Text: "why: " + body, Tone: "review-body"})
		}
	}
	if explanation := oneLine(answer.OverallExplanation); explanation != "" {
		lines = append(lines, reviewDisplayLine{Text: "overall: " + explanation, Tone: "review-overall"})
	}
	return lines, true
}

func reviewSummaryText(text string) string {
	answer, ok := parseReviewAnswer(text)
	if !ok {
		return oneLine(text)
	}
	status := fallback(strings.TrimSpace(answer.OverallCorrectness), "review complete")
	if len(answer.Findings) == 0 {
		return "review: " + status
	}
	return fmt.Sprintf("review: %s, %d findings, top %s", status, len(answer.Findings), reviewFindingHeadline(answer.Findings[0]))
}

func reviewFindingHeadline(finding reviewFinding) string {
	priority, hasPriority := reviewFindingPriority(finding)
	title := stripPriorityPrefix(oneLine(finding.Title))
	if title == "" {
		title = "Untitled finding"
	}
	priorityLabel := "P?"
	if hasPriority {
		priorityLabel = fmt.Sprintf("P%d", priority)
	}

	location := reviewLocation(finding.CodeLocation)
	if location != "" {
		return fmt.Sprintf("%s %s %s", priorityLabel, location, title)
	}
	return fmt.Sprintf("%s %s", priorityLabel, title)
}

func reviewFindingTone(finding reviewFinding) string {
	priority, ok := reviewFindingPriority(finding)
	if !ok {
		return "review-note"
	}
	switch {
	case priority <= 1:
		return "review-critical"
	case priority == 2:
		return "review-warning"
	default:
		return "review-note"
	}
}

func reviewFindingPriority(finding reviewFinding) (int, bool) {
	if finding.Priority != nil {
		return *finding.Priority, true
	}
	return priorityFromTitle(finding.Title)
}

func reviewLocation(location reviewCodeLocation) string {
	path := shortReviewPath(location.AbsoluteFilePath)
	if path == "" {
		return ""
	}
	if location.LineRange.Start > 0 {
		if location.LineRange.End > location.LineRange.Start {
			return fmt.Sprintf("%s:%d-%d", path, location.LineRange.Start, location.LineRange.End)
		}
		return fmt.Sprintf("%s:%d", path, location.LineRange.Start)
	}
	return path
}

func shortReviewPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	path = filepath.ToSlash(filepath.Clean(path))
	parts := strings.Split(path, "/")
	var kept []string
	for i := len(parts) - 1; i >= 0 && len(kept) < 3; i-- {
		if parts[i] == "" {
			continue
		}
		kept = append([]string{parts[i]}, kept...)
	}
	return strings.Join(kept, "/")
}

func stripPriorityPrefix(title string) string {
	title = strings.TrimSpace(title)
	if len(title) < 4 || title[0] != '[' {
		return title
	}
	end := strings.Index(title, "]")
	if end < 0 {
		return title
	}
	prefix := title[1:end]
	if len(prefix) >= 2 && prefix[0] == 'P' && prefix[1] >= '0' && prefix[1] <= '9' {
		return strings.TrimSpace(title[end+1:])
	}
	return title
}

func priorityFromTitle(title string) (int, bool) {
	title = strings.TrimSpace(title)
	if len(title) < 3 || title[0] != '[' || title[1] != 'P' || title[2] < '0' || title[2] > '9' {
		return 0, false
	}
	return int(title[2] - '0'), true
}
