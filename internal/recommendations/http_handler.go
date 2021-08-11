package recommendations

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi"
	"github.com/kristofferostlund/recommendli/pkg/logging"
	"github.com/kristofferostlund/recommendli/pkg/srv"
)

func NewRouter(svcFactory *ServiceFactory, auth *AuthAdaptor, log logging.Logger) *chi.Mux {
	handler := &httpHandler{svcFactory: svcFactory, auth: auth, log: log}
	r := chi.NewRouter()

	ar := r.With(auth.Middleware())
	ar.Get("/v1/whoami", handler.withService(handler.whoami))

	return r
}

type httpHandler struct {
	svcFactory *ServiceFactory
	auth       *AuthAdaptor
	log        logging.Logger
}

type spotifyClientHandlerFunc func(svc *Service) http.HandlerFunc

func (h *httpHandler) withService(sHandler spotifyClientHandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		spotifyClient, err := h.auth.GetClient(r)
		if err != nil && errors.Is(err, NoAuthenticationError) {
			srv.JSONError(w, fmt.Errorf("user not signed in: %w", err), srv.Status(http.StatusUnauthorized))
		} else if err != nil {
			h.log.Error("getting spotify client", err)
			srv.InternalServerError(w)
			return
		}

		svc := h.svcFactory.NewService(spotifyClient)
		sHandler(svc)(w, r)
	}
}

func (h *httpHandler) whoami(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		usr, err := svc.spotify.CurrentUser()
		if err != nil {
			http.Error(w, fmt.Sprintf("Internal server error: %s", err), 500)
			return
		}
		srv.JSON(w, usr)
	}
}
