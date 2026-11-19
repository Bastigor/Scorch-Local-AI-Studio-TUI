package theme

type ScorchState string

const (
	ScorchIdle     ScorchState = "idle"
	ScorchThinking ScorchState = "thinking"
	ScorchSuccess  ScorchState = "success"
	ScorchError    ScorchState = "error"
)

func RenderScorch(state ScorchState, frame int, reduceMotion bool) string {
	return ""
}
