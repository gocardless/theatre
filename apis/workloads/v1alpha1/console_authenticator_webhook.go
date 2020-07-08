/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

type consoleAuthenticator struct {
	logger  logr.Logger
	decoder *admission.Decoder
}

func NewConsoleAuthenticator(logger logr.Logger) *consoleAuthenticator {
	return &consoleAuthenticator{
		logger: logger,
	}
}

func (c *consoleAuthenticator) InjectDecoder(d *admission.Decoder) error {
	c.decoder = d
	return nil
}

// +kubebuilder:webhook:path=/mutate-workloads-crd-gocardless-com-v1alpha1-console,mutating=true,failurePolicy=fail,groups=workloads.crd.gocardless.com,resources=consoles,verbs=create,versions=v1alpha1,name=console-authenticator.crd.gocardless.com

func (c *consoleAuthenticator) Handle(ctx context.Context, req admission.Request) admission.Response {
	logger := c.logger.WithValues(c.logger, "uuid", string(req.UID))
	logger.Info("starting request", "event", "request.start")
	defer func(start time.Time) {
		logger.Info("completed request", "event", "request.end", "duration", time.Now().Sub(start).Seconds())
	}(time.Now())

	csl := &Console{}
	if err := c.decoder.Decode(req, csl); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	user := req.UserInfo.Username
	copy := csl.DeepCopy()
	copy.Spec.User = user

	copyBytes, err := json.Marshal(copy)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}

	logger.Info(fmt.Sprintf("authentication successful for user %s", user), "event", "authentication.success", "user", user)

	return admission.PatchResponseFromRaw(req.Object.Raw, copyBytes)
}
