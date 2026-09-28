package domain

// BuildVersion is the single source of truth for Coralline's build
// identifier, shared by the CLI's `version` command and the browse UI's
// sidebar. Named distinctly from the Version struct (a module version row)
// defined in types.go.
const BuildVersion = "v1.0.0-prototype"
