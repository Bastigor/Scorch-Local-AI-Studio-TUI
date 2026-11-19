package hardware

type BackendType string

const BackendCPU BackendType = "cpu"

type PrereqIssue struct {
	Name        string
	InstallHint string
}

type Info struct {
	OS                string
	Arch              string
	CPUName           string
	CPUCores          int
	TotalRAMBytes     uint64
	GPUName           string
	GPUVendor         string
	VRAMMB            int
	SelectedBackend   BackendType
	FallbackBackends  []BackendType
	Supported         bool
	UnsupportedReason string
	MissingPrereqs    []PrereqIssue
}

func Detect() Info {
	return Info{
		CPUName:         "CPU",
		GPUName:         "CPU",
		SelectedBackend: BackendCPU,
		Supported:       true,
	}
}
