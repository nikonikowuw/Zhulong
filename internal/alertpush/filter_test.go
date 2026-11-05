package alertpush

import "testing"

func TestDestinationFilterMatches(t *testing.T) {
	tests := []struct {
		name        string
		filter      *DestinationFilter
		eventType   string
		algorithmID string
		cameraID    string
		want        bool
	}{
		{
			name:        "nil filter matches all",
			filter:      nil,
			eventType:   "alarm",
			algorithmID: "algo_01",
			cameraID:    "cam_01",
			want:        true,
		},
		{
			name:        "empty filter matches all",
			filter:      &DestinationFilter{},
			eventType:   "face_recognition",
			algorithmID: "algo_01",
			cameraID:    "cam_01",
			want:        true,
		},
		{
			name: "match specific event type, algorithm and camera",
			filter: &DestinationFilter{
				EventTypes:   []string{"alarm", "face_recognition"},
				AlgorithmIDs: []string{"algo_01", "algo_02"},
				CameraIDs:    []string{"cam_01"},
			},
			eventType:   "alarm",
			algorithmID: "algo_01",
			cameraID:    "cam_01",
			want:        true,
		},
		{
			name: "event type mismatch",
			filter: &DestinationFilter{
				EventTypes: []string{"face_recognition"},
			},
			eventType:   "alarm",
			algorithmID: "algo_01",
			cameraID:    "cam_01",
			want:        false,
		},
		{
			name: "algorithm mismatch",
			filter: &DestinationFilter{
				AlgorithmIDs: []string{"algo_01"},
			},
			eventType:   "alarm",
			algorithmID: "algo_other",
			cameraID:    "cam_01",
			want:        false,
		},
		{
			name: "camera mismatch",
			filter: &DestinationFilter{
				CameraIDs: []string{"cam_01"},
			},
			eventType:   "alarm",
			algorithmID: "algo_01",
			cameraID:    "cam_other",
			want:        false,
		},
		{
			name: "empty algorithm id when filter is set",
			filter: &DestinationFilter{
				AlgorithmIDs: []string{"algo_01"},
			},
			eventType:   "alarm",
			algorithmID: "",
			cameraID:    "cam_01",
			want:        false,
		},
		{
			name: "empty camera id when filter is set",
			filter: &DestinationFilter{
				CameraIDs: []string{"cam_01"},
			},
			eventType:   "alarm",
			algorithmID: "algo_01",
			cameraID:    "",
			want:        false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.filter.Matches(tc.eventType, tc.algorithmID, tc.cameraID)
			if got != tc.want {
				t.Fatalf("expected Matches=%v, got %v", tc.want, got)
			}
		})
	}
}
