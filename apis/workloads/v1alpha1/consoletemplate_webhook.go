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
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

// log is for logging in this package.
var consoletemplatelog = logf.Log.WithName("consoletemplate-resource")

func (r *ConsoleTemplate) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

// +kubebuilder:webhook:verbs=create;update,path=/validate-workloads-crd-gocardless-com-v1alpha1-consoletemplate,mutating=false,failurePolicy=fail,groups=workloads.crd.gocardless.com,resources=consoletemplates,versions=v1alpha1,name=console-template-validation.crd.gocardless.com

var _ webhook.Validator = &ConsoleTemplate{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *ConsoleTemplate) ValidateCreate() error {
	logger := consoletemplatelog.WithValues("uuid", r.UID, "name", r.Name)
	logger.Info("starting request", "event", "request.start")

	defer func(start time.Time) {
		logger.Info("completed request", "event", "request.end", "duration", time.Now().Sub(start).Seconds())
	}(time.Now())

	if err := r.Validate(); err != nil {
		consoletemplatelog.Info("vailidation failed", "event", "validation.failure")
		return fmt.Errorf("the console template spec is invalid: %w", err)
	}

	logger.Info("validation successful", "event", "validation.success")
	return nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *ConsoleTemplate) ValidateUpdate(old runtime.Object) error {
	logger := consoletemplatelog.WithValues("uuid", r.UID, "name", r.Name)
	logger.Info("starting request", "event", "request.start")

	defer func(start time.Time) {
		logger.Info("completed request", "event", "request.end", "duration", time.Now().Sub(start).Seconds())
	}(time.Now())

	if err := r.Validate(); err != nil {
		consoletemplatelog.Info("validation failure", "event", "validation.failure")
		return fmt.Errorf("the console template spec is invalid: %w", err)
	}

	logger.Info("validation successful", "event", "validation.success")
	return nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *ConsoleTemplate) ValidateDelete() error {
	// we don't care about validation for delete
	return nil
}
