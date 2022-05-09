/*
Copyright 2022.

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

package controllers

import (
	"context"

	istioNetworkingv1alpha3 "istio.io/api/networking/v1alpha3"
	istioClientNetworkingv1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// IngressReconciler reconciles a Ingress object
type IngressReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const (
	IngressClassAnnotation = "kubernetes.io/ingress.class"
	IngressClassName       = "istio"
	FinalizerName          = "istio-acme-http.networking.synax.io/finalizer"
)

//+kubebuilder:rbac:groups=networking.istio.io,resources=virtualservices,verbs=create;get;delete
//+kubebuilder:rbac:groups=networking.istio.io,resources=virtualservices/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=networking.istio.io,resources=gateways,verbs=create;get;delete
//+kubebuilder:rbac:groups=networking.istio.io,resources=gateways/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch
//+kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses/finalizers,verbs=update
//+kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses/status,verbs=get;update;patch

func (r *IngressReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	log.Info("Processing Ingress", req.Name, req.Namespace)

	ingress := &networkingv1.Ingress{}
	if err := r.Get(ctx, req.NamespacedName, ingress); err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, "unable to fetch ingress")
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if isControllerIngressClass(ingress) {
		virtualService := ingressToVirtualService(ingress)
		err := r.Get(ctx, types.NamespacedName{Name: virtualService.Name, Namespace: virtualService.Namespace}, virtualService)
		if err != nil && errors.IsNotFound(err) {
			log.V(1).Info("Creating Virtual Service", "virtualservice", virtualService.Name)
			if err := controllerutil.SetControllerReference(ingress, virtualService, r.Scheme); err != nil {
				return ctrl.Result{}, err
			}
			err = r.Create(ctx, virtualService)
		}
		gateway := ingressToGateway(ingress)
		err = r.Get(ctx, types.NamespacedName{Name: gateway.Name, Namespace: gateway.Namespace}, gateway)
		if err != nil && errors.IsNotFound(err) {
			log.V(1).Info("Creating Gateway", "gateway", gateway.Name)
			if err := controllerutil.SetControllerReference(ingress, gateway, r.Scheme); err != nil {
				return ctrl.Result{}, err
			}
			err = r.Create(ctx, gateway)
		}

	}

	return ctrl.Result{}, nil
}

func isControllerIngressClass(ingress *networkingv1.Ingress) bool {
	if ingress.GetAnnotations()[IngressClassAnnotation] == IngressClassName {
		return true
	}
	return false
}

func ingressToGateway(ingress *networkingv1.Ingress) *istioClientNetworkingv1alpha3.Gateway {
	gw := &istioClientNetworkingv1alpha3.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ingress.Namespace,
			Name:      ingress.Name,
			Labels:    ingress.Labels,
		},
		Spec: istioNetworkingv1alpha3.Gateway{
			Selector: map[string]string{"istio": ingress.Namespace},
			Servers: []*istioNetworkingv1alpha3.Server{
				{
					Hosts: []string{ingress.Namespace + "/" + ingress.Spec.Rules[0].Host},
					Port: &istioNetworkingv1alpha3.Port{
						Name:     "http-istio-solver",
						Number:   80,
						Protocol: "HTTP",
					},
					Tls: &istioNetworkingv1alpha3.ServerTLSSettings{
						HttpsRedirect: false,
					},
				},
			},
		},
	}

	return gw
}

func ingressToVirtualService(ingress *networkingv1.Ingress) *istioClientNetworkingv1alpha3.VirtualService {
	vs := &istioClientNetworkingv1alpha3.VirtualService{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ingress.Namespace,
			Name:      ingress.Name,
			Labels:    ingress.Labels,
		},
		Spec: istioNetworkingv1alpha3.VirtualService{
			Gateways: []string{ingress.Namespace + "/" + ingress.Name},
			ExportTo: []string{"."},
			Hosts:    []string{ingress.Spec.Rules[0].Host},
			Http: []*istioNetworkingv1alpha3.HTTPRoute{
				{
					Match: []*istioNetworkingv1alpha3.HTTPMatchRequest{
						{
							Method: &istioNetworkingv1alpha3.StringMatch{
								MatchType: &istioNetworkingv1alpha3.StringMatch_Exact{
									Exact: ingress.Spec.Rules[0].HTTP.Paths[0].Path,
								},
							},
						},
					},
					Route: []*istioNetworkingv1alpha3.HTTPRouteDestination{
						{
							Destination: &istioNetworkingv1alpha3.Destination{
								Host: ingress.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name + "." + ingress.Namespace + ".svc.cluster.local",
								Port: &istioNetworkingv1alpha3.PortSelector{Number: uint32(ingress.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Port.Number)},
							},
						},
					},
				},
			},
		},
	}

	return vs

}

var (
	ownerKey = ".metadata.controller"
	apiGVstr = istioClientNetworkingv1alpha3.SchemeGroupVersion.String()
)

// SetupWithManager sets up the controller with the Manager.
func (r *IngressReconciler) SetupWithManager(mgr ctrl.Manager) error {

	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1.Ingress{}).
		Owns(&istioClientNetworkingv1alpha3.Gateway{}).
		Owns(&istioClientNetworkingv1alpha3.VirtualService{}).
		Complete(r)
}
