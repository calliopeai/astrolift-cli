package cmd

import "slices"

// The API returns a bounded tail, whose length stops growing while the task
// continues writing. Match its overlap with the prior snapshot, not its size.
type agentLogTail struct {
	previous []string
}

func (tail *agentLogTail) append(lines []string) []string {
	// An empty response can also mean temporarily unreadable pod logs.
	if len(lines) == 0 {
		return nil
	}
	previous := tail.previous
	tail.previous = lines
	for overlap := min(len(previous), len(lines)); overlap > 0; overlap-- {
		if slices.Equal(previous[len(previous)-overlap:], lines[:overlap]) {
			return lines[overlap:]
		}
	}
	return lines
}
