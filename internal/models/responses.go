package models

type AvailableEnvironmentResponse struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
}

type UserEnvironmentResponse struct {
	Name      string `json:"name"`
	UUID      string `json:"uuid"`
	Status    string `json:"status"`
	URL       string `json:"url"`
	ExpiresAt string `json:"expiresAt"`
	Icon      string `json:"icon"`
}

type UserEnvironmentsListResponse struct {
	Environments []UserEnvironmentResponse `json:"environments"`
	Count        int                       `json:"count"`
	Limit        int                       `json:"limit"`
}

type RunEnvironmentResponse struct {
	UUID      string `json:"uuid"`
	Status    string `json:"status"`
	URL       string `json:"url"`
	ExpiresAt string `json:"expiresAt"`
}

type StatusResponse struct {
	UUID      string `json:"uuid"`
	Status    string `json:"status"`
	URL       string `json:"url"`
	ExpiresAt string `json:"expiresAt"`
}

type ExtendResponse struct {
	ExpiresAt string `json:"expiresAt"`
}

type HealthResponse struct {
	Status string `json:"status"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}
