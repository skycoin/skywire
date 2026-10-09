//go:build gopus_dred || gopus_osce

package silk

// deepPLCSkipsRecoveryRamp matches the ENABLE_DEEP_PLC condition in
// silk/PLC.c:silk_PLC_glue_frames. Both DRED and OSCE reference builds enable
// deep PLC. At 16 kHz, deep PLC handles the recovery transition, so the
// ordinary SILK gain ramp is not applied.
func deepPLCSkipsRecoveryRamp(fsKHz int32) bool {
	return fsKHz == 16
}
