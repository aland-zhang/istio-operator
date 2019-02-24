/*
Copyright 2019 Banzai Cloud.

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

package config

import (
	"context"

	istiov1beta1 "github.com/banzaicloud/istio-operator/pkg/apis/operator/v1beta1"
	"github.com/banzaicloud/istio-operator/pkg/crds"
	"github.com/banzaicloud/istio-operator/pkg/resources"
	"github.com/banzaicloud/istio-operator/pkg/resources/citadel"
	"github.com/banzaicloud/istio-operator/pkg/resources/common"
	"github.com/banzaicloud/istio-operator/pkg/resources/galley"
	"github.com/banzaicloud/istio-operator/pkg/resources/gateways"
	"github.com/banzaicloud/istio-operator/pkg/resources/mixer"
	"github.com/banzaicloud/istio-operator/pkg/resources/pilot"
	"github.com/banzaicloud/istio-operator/pkg/resources/sidecarinjector"
	"github.com/go-logr/logr"
	"github.com/goph/emperror"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("controller")

// Add creates a new Config Controller and adds it to the Manager with default RBAC. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	dynamic, err := dynamic.NewForConfig(mgr.GetConfig())
	if err != nil {
		return emperror.Wrap(err, "failed to create dynamic client")
	}
	crd, err := crds.New(mgr.GetConfig(), crds.InitCrds())
	if err != nil {
		return emperror.Wrap(err, "unable to set up crd operator")
	}
	return add(mgr, newReconciler(mgr, dynamic, crd))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager, d dynamic.Interface, crd *crds.CrdOperator) reconcile.Reconciler {
	return &ReconcileConfig{
		Client:      mgr.GetClient(),
		dynamic:     d,
		crdOperator: crd,
	}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("config-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to Config
	err = c.Watch(&source.Kind{Type: &istiov1beta1.Config{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	//
	//// TODO(user): Modify this to be the types you create
	//// Uncomment watch a Deployment created by Config - change this for objects you create
	//err = c.Watch(&source.Kind{Type: &appsv1.Deployment{}}, &handler.EnqueueRequestForOwner{
	//	IsController: true,
	//	OwnerType:    &istiov1beta1.Config{},
	//})
	//if err != nil {
	//	return err
	//}

	return nil
}

var _ reconcile.Reconciler = &ReconcileConfig{}

// ReconcileConfig reconciles a Config object
type ReconcileConfig struct {
	client.Client
	dynamic     dynamic.Interface
	crdOperator *crds.CrdOperator
}

type ReconcileComponent func(log logr.Logger, istio *istiov1beta1.Config) error

// Reconcile reads that state of the cluster for a Config object and makes changes based on the state read
// and what is in the Config.Spec
// Automatically generate RBAC rules to allow the Controller to read and write Deployments
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=operator.operator.io,resources=configs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=operator.operator.io,resources=configs/status,verbs=get;update;patch
func (r *ReconcileConfig) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling Istio")
	// Fetch the Config instance
	instance := &istiov1beta1.Config{}
	err := r.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Object not found, return.  Created objects are automatically garbage collected.
			// For additional cleanup logic use finalizers.
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	log.Info("creating CRDs")
	err = r.crdOperator.Reconcile(instance)
	if err != nil {
		log.Error(err, "unable to initialize CRDs")
		return reconcile.Result{}, err
	}

	reconcilers := []resources.ComponentReconciler{
		common.New(r.Client, instance),
		citadel.New(citadel.Configuration{
			DeployMeshPolicy: true,
			SelfSignedCA:     true,
		}, r.Client, r.dynamic, instance),
		galley.New(r.Client, instance),
		pilot.New(r.Client, r.dynamic, instance),
		gateways.New(r.Client, instance),
		mixer.New(r.Client, r.dynamic, instance),
		sidecarinjector.New(sidecarinjector.Configuration{
			IncludeIPRanges: instance.Spec.IncludeIPRanges,
			ExcludeIPRanges: instance.Spec.ExcludeIPRanges,
		}, r.Client, instance),
	}

	for _, rec := range reconcilers {
		err = rec.Reconcile(reqLogger)
		if err != nil {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}
