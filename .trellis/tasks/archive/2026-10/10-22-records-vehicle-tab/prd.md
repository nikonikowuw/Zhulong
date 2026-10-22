# Add vehicle tab to records

## Goal

Add a vehicle-specific tab under Records using existing vehicle captures, showing both crop and full-scene imagery in the list and detail.

## Requirements

- Add a fourth `vehicles` tab to the Records page for vehicle-structure captures.
- Query the existing captures endpoint with `targetType=vehicle`; do not add a backend endpoint, database field, or migration.
- Keep vehicle-specific inference values (plate number/color, vehicle type/color, and nested plate data) in the existing structured `attributes` payload and detail area. Do not add them as table columns or card metadata.
- Show both the vehicle crop and full-scene image in each vehicle list item, grouped as visual evidence rather than separate data columns. Opening an item must retain the existing detail view with the full scene and vehicle bounding box, the crop image, and attributes.
- Preserve existing Records behavior for recognitions, alarms, and generic captures. Keep the selected tab in the validated URL search state and reset tab-specific filters when changing tabs.
- Localize new visible labels and image accessibility text in English, Simplified Chinese, and Traditional Chinese.
- Cover the vehicle filtering, both image previews, detail selection, and existing-tab regression behavior with browser tests.

## Constraints

- No server-side plate-number search in this task; it can be added separately if required.
- No vehicle catalog, CRUD API, live-video overlay, or model/data-contract changes.
- Use existing capture paths (`cropPath` and `imagePath`) and the current Records detail dialog.

## Acceptance Criteria

- [x] Records has a fourth Vehicles tab and the URL schema accepts and preserves it.
- [x] The Vehicles tab requests only captures whose `targetType` is `vehicle`, with camera/date filters and pagination working as before.
- [x] Vehicle list entries show both crop and full-scene previews without turning vehicle attributes into columns.
- [x] Selecting a vehicle opens the existing detail flow with full-scene bounding box, crop image, and structured attributes.
- [x] Loading, error/retry, and empty states are covered; existing three tabs retain their behavior.
- [x] New labels are present in all three locales and targeted browser tests plus frontend lint/build pass.

## Notes

- Keep `prd.md` focused on requirements, constraints, and acceptance criteria.
- Lightweight tasks can remain PRD-only.
- For complex tasks, add `design.md` for technical design and `implement.md` for execution planning before `task.py start`.
