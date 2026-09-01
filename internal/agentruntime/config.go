package agentruntime

type RuntimeMode string

const (
	RuntimeModeDevelopment RuntimeMode = "development"
	RuntimeModeProduction  RuntimeMode = "production"
)

type Config struct {
	StoreProvider  StoreProvider
	PolicyEngine   PolicyEngine
	PolicyEnforcer PolicyObligationEnforcer
	OutputGateway  OutputGateway
	Pipeline       PipelineComponents
}

func DefaultConfig() Config { return Config{} }
