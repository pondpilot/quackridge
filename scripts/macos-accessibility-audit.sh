#!/bin/sh
set -eu

views=macos/QuackRidge/Views
rg -q '\.keyboardShortcut\(\.defaultAction\)' "$views"
rg -q '\.keyboardShortcut\(\.cancelAction\)' "$views"
rg -q '\.accessibilityLabel\(' "$views"
rg -q '\.accessibilityIdentifier\(' "$views"
rg -q '\.accessibilityElement\(' "$views"
rg -q 'SecureField\(' "$views/SourceWizardView.swift"
rg -q '\.privacySensitive\(\)' "$views/SourceWizardView.swift"

echo "macOS static accessibility checks passed; manual VoiceOver and appearance acceptance remains a release gate"
