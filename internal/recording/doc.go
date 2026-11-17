// Package recording owns event clip recording: the event_clips table, triggering
// clips on the native recorder through a narrow ClipHub port, polling completion
// events, start-up repair of interrupted clips and row cleanup.
//
// The package only exposes pure constructors; it never imports internal/engine,
// internal/camera or internal/task. The app layer adapts camera.StreamHub /
// engine.Engine to ClipHub and storage to MediaRootProvider / WriteGate.
package recording
