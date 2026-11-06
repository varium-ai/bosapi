//go:build !linux && !darwin

package telemetry

func readProcess() processReading { return processReading{} }
