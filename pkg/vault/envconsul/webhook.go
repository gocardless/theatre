package envconsul

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

	kitlog "github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"

	"k8s.io/api/admission/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"

	"sigs.k8s.io/controller-runtime/pkg/webhook/admission/types"
)

type AdmissionWebhook struct {
	logger  kitlog.Logger
	decoder runtime.Decoder
}

func NewAdmissionWebhook(logger kitlog.Logger) *AdmissionWebhook {
	scheme := runtime.NewScheme()
	codecs := serializer.NewCodecFactory(scheme)

	return &AdmissionWebhook{
		logger:  kitlog.With(logger, "component", "AdmissionWebhook"),
		decoder: codecs.UniversalDeserializer(),
	}
}

func (wh *AdmissionWebhook) Handle(admissionFunc func(ctx context.Context, req types.Request) types.Response) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		if data, err := ioutil.ReadAll(r.Body); err == nil {
			body = data
		}

		var admissionResponse *v1beta1.AdmissionResponse
		ar := v1beta1.AdmissionReview{}
		if _, _, err := wh.decoder.Decode(body, nil, &ar); err != nil {
			level.Error(wh.logger).Log("event", "decode_request.failed", "msg", err)
			admissionResponse = &v1beta1.AdmissionResponse{
				Result: &metav1.Status{
					Message: err.Error(),
				},
			}
		} else {
			admissionResponse = admissionFunc(r.Context(), types.Request{AdmissionRequest: ar.Request}).Response
		}

		response, err := json.Marshal(v1beta1.AdmissionReview{Response: admissionResponse})
		if err != nil {
			level.Error(wh.logger).Log("event", "encode_response.failed", "msg", err)
			http.Error(w, fmt.Sprintf("failed to encode webhook response: %v", err), http.StatusInternalServerError)
		}
		if _, err := w.Write(response); err != nil {
			level.Error(wh.logger).Log("event", "encode_response.failed", "msg", err)
			http.Error(w, fmt.Sprintf("failed to write webhook response: %v", err), http.StatusInternalServerError)
		}
	})
}
