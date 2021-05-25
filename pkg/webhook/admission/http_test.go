/*
Copyright 2018 The Kubernetes Authors.

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

package admission

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	admissionv1 "k8s.io/api/admission/v1"

	logf "sigs.k8s.io/controller-runtime/pkg/internal/log"
	"sigs.k8s.io/controller-runtime/pkg/runtime/inject"
)

var _ = Describe("Admission Webhooks", func() {

	const (
		gvkJSONv1      = `"kind":"AdmissionReview","apiVersion":"admission.k8s.io/v1"`
		gvkJSONv1beta1 = `"kind":"AdmissionReview","apiVersion":"admission.k8s.io/v1beta1"`
	)

	Describe("HTTP Handler", func() {
		var respRecorder *httptest.ResponseRecorder
		webhook := &Webhook{
			Handler: nil,
		}
		BeforeEach(func() {
			respRecorder = &httptest.ResponseRecorder{
				Body: bytes.NewBuffer(nil),
			}
			_, err := inject.LoggerInto(log.WithName("test-webhook"), webhook)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should return bad-request when given an empty body", func() {
			req := &http.Request{Body: nil}

			expected := fmt.Sprintf(`{%s,"response":{"uid":"","allowed":false,"status":{"metadata":{},"message":"request body is empty","code":400}}}
`, gvkJSONv1)

			webhook.ServeHTTP(respRecorder, req)
			Expect(respRecorder.Body.String()).To(Equal(expected))
		})

		It("should return bad-request when given the wrong content-type", func() {
			req := &http.Request{
				Header: http.Header{"Content-Type": []string{"application/foo"}},
				Body:   nopCloser{Reader: bytes.NewBuffer(nil)},
			}

			expected := fmt.Sprintf(`{%s,"response":{"uid":"","allowed":false,"status":{"metadata":{},`+
				`"message":"contentType=application/foo, expected application/json","code":400}}}
`, gvkJSONv1)

			webhook.ServeHTTP(respRecorder, req)
			Expect(respRecorder.Body.String()).To(Equal(expected))
		})

		It("should return bad-request when given an undecodable body", func() {
			req := &http.Request{
				Header: http.Header{"Content-Type": []string{"application/json"}},
				Body:   nopCloser{Reader: bytes.NewBufferString("{")},
			}

			expected := fmt.Sprintf(`{%s,"response":{"uid":"","allowed":false,"status":{"metadata":{},`+
				`"message":"couldn't get version/kind; json parse error: unexpected end of JSON input","code":400}}}
`, gvkJSONv1)

			webhook.ServeHTTP(respRecorder, req)
			Expect(respRecorder.Body.String()).To(Equal(expected))
		})

		It("should return the response given by the handler with version defaulted to v1", func() {
			req := &http.Request{
				Header: http.Header{"Content-Type": []string{"application/json"}},
				Body:   nopCloser{Reader: bytes.NewBufferString(`{"request":{}}`)},
			}
			webhook := &Webhook{
				Handler: &fakeHandler{},
				log:     logf.RuntimeLog.WithName("webhook"),
			}

			expected := fmt.Sprintf(`{%s,"response":{"uid":"","allowed":true,"status":{"metadata":{},"code":200}}}
`, gvkJSONv1)
			webhook.ServeHTTP(respRecorder, req)
			Expect(respRecorder.Body.String()).To(Equal(expected))
		})

		It("should return the v1 response given by the handler", func() {
			req := &http.Request{
				Header: http.Header{"Content-Type": []string{"application/json"}},
				Body:   nopCloser{Reader: bytes.NewBufferString(fmt.Sprintf(`{%s,"request":{}}`, gvkJSONv1))},
			}
			webhook := &Webhook{
				Handler: &fakeHandler{},
				log:     logf.RuntimeLog.WithName("webhook"),
			}

			expected := fmt.Sprintf(`{%s,"response":{"uid":"","allowed":true,"status":{"metadata":{},"code":200}}}
`, gvkJSONv1)
			webhook.ServeHTTP(respRecorder, req)
			Expect(respRecorder.Body.String()).To(Equal(expected))
		})

		It("should return the v1beta1 response given by the handler", func() {
			req := &http.Request{
				Header: http.Header{"Content-Type": []string{"application/json"}},
				Body:   nopCloser{Reader: bytes.NewBufferString(fmt.Sprintf(`{%s,"request":{}}`, gvkJSONv1beta1))},
			}
			webhook := &Webhook{
				Handler: &fakeHandler{},
				log:     logf.RuntimeLog.WithName("webhook"),
			}

			expected := fmt.Sprintf(`{%s,"response":{"uid":"","allowed":true,"status":{"metadata":{},"code":200}}}
`, gvkJSONv1beta1)
			webhook.ServeHTTP(respRecorder, req)
			Expect(respRecorder.Body.String()).To(Equal(expected))
		})

		It("should present the Context from the HTTP request, if any", func() {
			req := &http.Request{
				Header: http.Header{"Content-Type": []string{"application/json"}},
				Body:   nopCloser{Reader: bytes.NewBufferString(`{"request":{}}`)},
			}
			type ctxkey int
			const key ctxkey = 1
			const value = "from-ctx"
			webhook := &Webhook{
				Handler: &fakeHandler{
					fn: func(ctx context.Context, req Request) Response {
						<-ctx.Done()
						return Allowed(ctx.Value(key).(string))
					},
				},
				log: logf.RuntimeLog.WithName("webhook"),
			}

			expected := fmt.Sprintf(`{%s,"response":{"uid":"","allowed":true,"status":{"metadata":{},"reason":%q,"code":200}}}
`, gvkJSONv1, value)

			ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key, value))
			cancel()
			webhook.ServeHTTP(respRecorder, req.WithContext(ctx))
			Expect(respRecorder.Body.String()).To(Equal(expected))
		})

		It("should mutate the Context from the HTTP request, if func supplied", func() {
			req := &http.Request{
				Header: http.Header{"Content-Type": []string{"application/json"}},
				Body:   nopCloser{Reader: bytes.NewBufferString(`{"request":{}}`)},
			}
			type ctxkey int
			const key ctxkey = 1
			webhook := &Webhook{
				Handler: &fakeHandler{
					fn: func(ctx context.Context, req Request) Response {
						return Allowed(ctx.Value(key).(string))
					},
				},
				WithContextFunc: func(ctx context.Context, r *http.Request) context.Context {
					return context.WithValue(ctx, key, r.Header["Content-Type"][0])
				},
				log: logf.RuntimeLog.WithName("webhook"),
			}

			expected := fmt.Sprintf(`{%s,"response":{"uid":"","allowed":true,"status":{"metadata":{},"reason":%q,"code":200}}}
`, gvkJSONv1, "application/json")

			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			webhook.ServeHTTP(respRecorder, req.WithContext(ctx))
			Expect(respRecorder.Body.String()).To(Equal(expected))
		})
	})
})

type nopCloser struct {
	io.Reader
}

func (nopCloser) Close() error { return nil }

type fakeHandler struct {
	invoked        bool
	fn             func(context.Context, Request) Response
	decoder        *Decoder
	injectedString string
}

func (h *fakeHandler) InjectDecoder(d *Decoder) error {
	h.decoder = d
	return nil
}

func (h *fakeHandler) InjectString(s string) error {
	h.injectedString = s
	return nil
}

func (h *fakeHandler) Handle(ctx context.Context, req Request) Response {
	h.invoked = true
	if h.fn != nil {
		return h.fn(ctx, req)
	}
	return Response{AdmissionResponse: admissionv1.AdmissionResponse{
		Allowed: true,
	}}
}
