package schedule

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// MinutesPerDay is the total number of minutes in a 24-hour day.
const MinutesPerDay = 1440

// MinutesPerWeek is the total number of minutes in a 7-day week.
const MinutesPerWeek = 7 * MinutesPerDay

// DayKeys defines the canonical JSON keys for the 7 days of the week,
// indexed by time.Weekday (0 = Sunday, 1 = Monday, ..., 6 = Saturday).
var DayKeys = [7]string{
	"sunday",
	"monday",
	"tuesday",
	"wednesday",
	"thursday",
	"friday",
	"saturday",
}

// TimeRange represents an inclusive-start, exclusive-end time interval [Start, End)
// within a single day, expressed as "HH:mm" or "HH:mm:ss" strings (e.g. "08:30", "17:00").
type TimeRange struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// WeekSchedule defines weekly recurring active time windows.
// When Enabled is false or Days is empty, the schedule represents 24/7 (always active).
type WeekSchedule struct {
	Enabled bool                   `json:"enabled"`
	Days    map[string][]TimeRange `json:"days,omitempty"`
}

// Interval represents an active time window [StartMinute, EndMinute) in minutes.
type Interval struct {
	StartMinute int
	EndMinute   int
}

// Parse parses and normalizes a schedule JSON string.
// An empty string, whitespace, or "{}" yields a valid schedule with Enabled=false (24/7).
func Parse(raw string) (*WeekSchedule, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "{}" {
		return &WeekSchedule{
			Enabled: false,
			Days:    make(map[string][]TimeRange),
		}, nil
	}

	var s WeekSchedule
	if err := json.Unmarshal([]byte(trimmed), &s); err != nil {
		return nil, fmt.Errorf("invalid schedule json: %w", err)
	}

	if err := s.Normalize(); err != nil {
		return nil, err
	}

	return &s, nil
}

// IsAlwaysActive returns true if the schedule is disabled or nil (representing 24/7 unconstrained operation).
func (s *WeekSchedule) IsAlwaysActive() bool {
	if s == nil || !s.Enabled {
		return true
	}
	return false
}

// Normalize validates and normalizes all daily time ranges:
// 1. Validates time formats and range bounds (00:00 <= start < end <= 24:00);
// 2. Sorts ranges by start time;
// 3. Merges overlapping or contiguous ranges.
func (s *WeekSchedule) Normalize() error {
	if s == nil {
		return nil
	}
	if s.Days == nil {
		s.Days = make(map[string][]TimeRange)
		return nil
	}

	normalizedDays := make(map[string][]TimeRange, len(s.Days))
	for dayKey, ranges := range s.Days {
		canonicalKey := normalizeDayKey(dayKey)
		if canonicalKey == "" {
			return fmt.Errorf("invalid day key: %q", dayKey)
		}

		intervals := make([]Interval, 0, len(ranges))
		for _, r := range ranges {
			startMin, err := parseTimeToMinutes(r.Start)
			if err != nil {
				return fmt.Errorf("invalid start time %q for %s: %w", r.Start, dayKey, err)
			}
			endMin, err := parseTimeToMinutes(r.End)
			if err != nil {
				return fmt.Errorf("invalid end time %q for %s: %w", r.End, dayKey, err)
			}
			if startMin >= endMin {
				return fmt.Errorf("start time %q must be strictly before end time %q for %s", r.Start, r.End, dayKey)
			}
			intervals = append(intervals, Interval{StartMinute: startMin, EndMinute: endMin})
		}

		merged := mergeIntervals(intervals)
		normalizedRanges := make([]TimeRange, 0, len(merged))
		for _, m := range merged {
			normalizedRanges = append(normalizedRanges, TimeRange{
				Start: minutesToTimeString(m.StartMinute),
				End:   minutesToTimeString(m.EndMinute),
			})
		}
		if len(normalizedRanges) > 0 {
			normalizedDays[canonicalKey] = normalizedRanges
		}
	}

	s.Days = normalizedDays
	return nil
}

// IsActiveAt reports whether the schedule is active at the given localized time.
func (s *WeekSchedule) IsActiveAt(t time.Time) bool {
	if s.IsAlwaysActive() {
		return true
	}

	weekday := t.Weekday()
	dayKey := DayKeys[weekday]
	ranges, ok := s.Days[dayKey]
	if !ok || len(ranges) == 0 {
		return false
	}

	currentMinute := t.Hour()*60 + t.Minute()
	for _, r := range ranges {
		startMin, _ := parseTimeToMinutes(r.Start)
		endMin, _ := parseTimeToMinutes(r.End)
		if currentMinute >= startMin && currentMinute < endMin {
			return true
		}
	}

	return false
}

// WeeklyIntervals returns all active intervals over a full 7-day week [0, 10080) minutes,
// aligned with time.Weekday (Sunday = 0, Monday = 1, ..., Saturday = 6).
func (s *WeekSchedule) WeeklyIntervals() []Interval {
	if s.IsAlwaysActive() {
		return []Interval{{StartMinute: 0, EndMinute: MinutesPerWeek}}
	}

	var all []Interval
	for weekdayIdx, dayKey := range DayKeys {
		ranges := s.Days[dayKey]
		dayOffset := weekdayIdx * MinutesPerDay
		for _, r := range ranges {
			startMin, _ := parseTimeToMinutes(r.Start)
			endMin, _ := parseTimeToMinutes(r.End)
			all = append(all, Interval{
				StartMinute: dayOffset + startMin,
				EndMinute:   dayOffset + endMin,
			})
		}
	}

	return mergeIntervals(all)
}

// SchedulableItem describes an entity consuming compute points according to a schedule.
type SchedulableItem struct {
	Points   int64
	Schedule *WeekSchedule
}

type sweepEvent struct {
	minute int
	delta  int64
}

// CalculatePeakPoints calculates the maximum compute points required at any moment in the week
// using a sweep-line algorithm over the 7x24h time axis.
func CalculatePeakPoints(items []SchedulableItem) int64 {
	var basePoints int64
	var events []sweepEvent

	for _, item := range items {
		if item.Points <= 0 {
			continue
		}
		if item.Schedule.IsAlwaysActive() {
			basePoints += item.Points
			continue
		}

		intervals := item.Schedule.WeeklyIntervals()
		for _, iv := range intervals {
			events = append(events,
				sweepEvent{minute: iv.StartMinute, delta: item.Points},
				sweepEvent{minute: iv.EndMinute, delta: -item.Points},
			)
		}
	}

	if len(events) == 0 {
		return basePoints
	}

	// Sort events:
	// 1. By minute ascending;
	// 2. If same minute, negative delta (interval ending) before positive delta (interval starting)
	//    so that handover between adjacent intervals does not create a false transient spike.
	sort.Slice(events, func(i, j int) bool {
		if events[i].minute != events[j].minute {
			return events[i].minute < events[j].minute
		}
		return events[i].delta < events[j].delta
	})

	current := basePoints
	peak := basePoints

	for _, ev := range events {
		current += ev.delta
		if current > peak {
			peak = current
		}
	}

	return peak
}

func normalizeDayKey(raw string) string {
	lower := strings.ToLower(strings.TrimSpace(raw))
	switch lower {
	case "0", "sun", "sunday":
		return "sunday"
	case "1", "mon", "monday":
		return "monday"
	case "2", "tue", "tues", "tuesday":
		return "tuesday"
	case "3", "wed", "wednesday":
		return "wednesday"
	case "4", "thu", "thur", "thurs", "thursday":
		return "thursday"
	case "5", "fri", "friday":
		return "friday"
	case "6", "sat", "saturday":
		return "saturday"
	default:
		return ""
	}
}

func parseTimeToMinutes(s string) (int, error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, fmt.Errorf("invalid time format, expected HH:mm or HH:mm:ss")
	}

	hour, err := strconv.Atoi(parts[0])
	if err != nil || hour < 0 || hour > 24 {
		return 0, fmt.Errorf("invalid hour %q", parts[0])
	}
	min, err := strconv.Atoi(parts[1])
	if err != nil || min < 0 || min > 59 {
		return 0, fmt.Errorf("invalid minute %q", parts[1])
	}

	total := hour*60 + min
	if hour == 24 {
		if min != 0 {
			return 0, fmt.Errorf("24:00 is max allowed bound, got %s", s)
		}
		total = MinutesPerDay
	}

	return total, nil
}

func minutesToTimeString(m int) string {
	if m == MinutesPerDay {
		return "24:00"
	}
	hour := m / 60
	min := m % 60
	return fmt.Sprintf("%02d:%02d", hour, min)
}

func mergeIntervals(intervals []Interval) []Interval {
	if len(intervals) <= 1 {
		return intervals
	}

	sort.Slice(intervals, func(i, j int) bool {
		if intervals[i].StartMinute != intervals[j].StartMinute {
			return intervals[i].StartMinute < intervals[j].StartMinute
		}
		return intervals[i].EndMinute < intervals[j].EndMinute
	})

	merged := make([]Interval, 0, len(intervals))
	curr := intervals[0]

	for i := 1; i < len(intervals); i++ {
		next := intervals[i]
		if next.StartMinute <= curr.EndMinute {
			// Overlapping or contiguous: extend current end
			if next.EndMinute > curr.EndMinute {
				curr.EndMinute = next.EndMinute
			}
		} else {
			merged = append(merged, curr)
			curr = next
		}
	}
	merged = append(merged, curr)
	return merged
}
