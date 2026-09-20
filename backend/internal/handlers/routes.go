package handlers

import (
	"net/http"

	"github.com/livant05/rrhh-go/internal/auth"
)

// Routes builds the full API mux. Every tenant-scoped route lives on one
// `protected` sub-mux mounted once behind signer.Middleware: omitting a
// route from `protected` yields a 404 (fail-closed), instead of the
// fail-open risk of forgetting to wrap an individual route in middleware.
func (a *API) Routes(signer *auth.Signer) http.Handler {
	protected := http.NewServeMux()
	protected.HandleFunc("GET /api/me", a.Me)

	protected.HandleFunc("GET /api/departments", a.ListDepartments)
	protected.HandleFunc("POST /api/departments", a.CreateDepartment)
	protected.HandleFunc("GET /api/departments/{id}", a.GetDepartment)
	protected.HandleFunc("PATCH /api/departments/{id}", a.UpdateDepartment)
	protected.HandleFunc("DELETE /api/departments/{id}", a.DeleteDepartment)

	protected.HandleFunc("GET /api/positions", a.ListPositions)
	protected.HandleFunc("POST /api/positions", a.CreatePosition)
	protected.HandleFunc("GET /api/positions/{id}", a.GetPosition)
	protected.HandleFunc("PATCH /api/positions/{id}", a.UpdatePosition)
	protected.HandleFunc("DELETE /api/positions/{id}", a.DeletePosition)

	protected.HandleFunc("GET /api/branches", a.ListBranches)
	protected.HandleFunc("POST /api/branches", a.CreateBranch)
	protected.HandleFunc("GET /api/branches/{id}", a.GetBranch)
	protected.HandleFunc("PATCH /api/branches/{id}", a.UpdateBranch)
	protected.HandleFunc("DELETE /api/branches/{id}", a.DeleteBranch)

	protected.HandleFunc("GET /api/roles", a.ListRoles)
	protected.HandleFunc("POST /api/roles", a.CreateRole)
	protected.HandleFunc("GET /api/roles/{id}", a.GetRole)
	protected.HandleFunc("PATCH /api/roles/{id}", a.UpdateRole)
	protected.HandleFunc("DELETE /api/roles/{id}", a.DeleteRole)

	protected.HandleFunc("GET /api/employees", a.ListEmployees)
	protected.HandleFunc("POST /api/employees", a.CreateEmployee)
	protected.HandleFunc("GET /api/employees/{id}", a.GetEmployee)
	protected.HandleFunc("PATCH /api/employees/{id}", a.UpdateEmployee)
	protected.HandleFunc("DELETE /api/employees/{id}", a.DeleteEmployee) // soft delete (P6.2)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", a.Health)     // public — more specific than /api/
	mux.HandleFunc("POST /api/auth/login", a.Login) // public — more specific than /api/
	mux.Handle("/api/", signer.Middleware(protected))
	return mux
}
