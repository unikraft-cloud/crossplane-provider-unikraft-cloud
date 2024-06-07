/*
Copyright 2022 The Crossplane Authors.

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

package instance

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/connection"
	"github.com/crossplane/crossplane-runtime/pkg/controller"
	"github.com/crossplane/crossplane-runtime/pkg/event"
	"github.com/crossplane/crossplane-runtime/pkg/meta"
	"github.com/crossplane/crossplane-runtime/pkg/ratelimiter"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	k8resource "k8s.io/apimachinery/pkg/api/resource"

	"github.com/crossplane/provider-kraftcloud/apis/compute/v1alpha1"
	apisv1alpha1 "github.com/crossplane/provider-kraftcloud/apis/v1alpha1"
	"github.com/crossplane/provider-kraftcloud/internal/features"
	kraftcloud "sdk.kraft.cloud"
	kraftcloudinstances "sdk.kraft.cloud/instances"
	kraftcloudservices "sdk.kraft.cloud/services"
)

const (
	errNotInstance  = "managed resource is not a Instance custom resource"
	errTrackPCUsage = "cannot track ProviderConfig usage"
	errGetPC        = "cannot get ProviderConfig"
	errGetCreds     = "cannot get credentials"

	errNewClient = "cannot create new Service"
)

var kraftCloudSDKFromCreds = func(token []byte) (kraftcloudinstances.InstancesService, error) {
	return kraftcloud.NewInstancesClient(
		kraftcloud.WithToken(string(token)),
	), nil
}

// Setup adds a controller that reconciles Instance managed resources.
func Setup(mgr ctrl.Manager, o controller.Options) error {
	name := managed.ControllerName(v1alpha1.InstanceGroupKind)

	cps := []managed.ConnectionPublisher{managed.NewAPISecretPublisher(mgr.GetClient(), mgr.GetScheme())}
	if o.Features.Enabled(features.EnableAlphaExternalSecretStores) {
		cps = append(cps, connection.NewDetailsManager(mgr.GetClient(), apisv1alpha1.StoreConfigGroupVersionKind))
	}

	r := managed.NewReconciler(mgr,
		resource.ManagedKind(v1alpha1.InstanceGroupVersionKind),
		managed.WithExternalConnecter(&connector{
			kube:         mgr.GetClient(),
			usage:        resource.NewProviderConfigUsageTracker(mgr.GetClient(), &apisv1alpha1.ProviderConfigUsage{}),
			newServiceFn: kraftCloudSDKFromCreds,
		}),
		managed.WithLogger(o.Logger.WithValues("controller", name)),
		managed.WithPollInterval(o.PollInterval),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))),
		managed.WithConnectionPublishers(cps...))

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		WithOptions(o.ForControllerRuntime()).
		WithEventFilter(resource.DesiredStateChanged()).
		For(&v1alpha1.Instance{}).
		Complete(ratelimiter.NewReconciler(name, r, o.GlobalRateLimiter))
}

// A connector is expected to produce an ExternalClient when its Connect method
// is called.
type connector struct {
	kube         client.Client
	usage        resource.Tracker
	newServiceFn func(creds []byte) (kraftcloudinstances.InstancesService, error)
}

// Connect typically produces an ExternalClient by:
// 1. Tracking that the managed resource is using a ProviderConfig.
// 2. Getting the managed resource's ProviderConfig.
// 3. Getting the credentials specified by the ProviderConfig.
// 4. Using the credentials to form a client.
func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*v1alpha1.Instance)
	if !ok {
		return nil, errors.New(errNotInstance)
	}

	if err := c.usage.Track(ctx, mg); err != nil {
		return nil, errors.Wrap(err, errTrackPCUsage)
	}

	pc := &apisv1alpha1.ProviderConfig{}
	if err := c.kube.Get(ctx, types.NamespacedName{Name: cr.GetProviderConfigReference().Name}, pc); err != nil {
		return nil, errors.Wrap(err, errGetPC)
	}

	// TODO(jake-ciolek): Handle metros in connection details.
	cd := pc.Spec.Credentials
	data, err := resource.CommonCredentialExtractor(ctx, cd.Source, c.kube, cd.CommonCredentialSelectors)
	if err != nil {
		return nil, errors.Wrap(err, errGetCreds)
	}

	svc, err := c.newServiceFn(data)
	if err != nil {
		return nil, errors.Wrap(err, errNewClient)
	}

	return &kraftcloudClient{client: svc}, nil
}

// An ExternalClient observes, then either creates, updates, or deletes an
// external resource to ensure it reflects the managed resource's desired state.
type kraftcloudClient struct {
	// A 'client' used to connect to the external resource API. In practice this
	// would be something like an AWS SDK client.
	client kraftcloudinstances.InstancesService
}

func (c *kraftcloudClient) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*v1alpha1.Instance)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotInstance)
	}
	instanceRaw, err := c.client.WithMetro(cr.Spec.ForProvider.Metro).Get(ctx, meta.GetExternalName(mg))
	// TODO(jake-ciolek): Currently, we take all errors to mean the instance doesn't exist.
	//                    This doesn't need to be true. API can fail and we need to handle that.
	// nolint:nilerr
	if err != nil {
		return managed.ExternalObservation{
			ResourceExists:    false,
			ResourceUpToDate:  false,
			ConnectionDetails: managed.ConnectionDetails{},
		}, nil
	}

	instance := instanceRaw.Data.Entries[0]

	cr.Status.AtProvider = v1alpha1.InstanceObservation{
		BootTime:  int64(instance.BootTimeUs),
		CreatedAt: instance.CreatedAt,
		PrivateIP: instance.PrivateIP,
	}

	if instance.ServiceGroup != nil && len(instance.ServiceGroup.Domains) > 0 {
		cr.Status.AtProvider.DNS = instance.ServiceGroup.Domains[0].FQDN
	}

	cr.Status.SetConditions(v1.Available())

	// Make sure the instance state matches our desired state.
	// If not, force a reconciliation to happen.
	return managed.ExternalObservation{
		ResourceExists:    true,
		ResourceUpToDate:  string(instance.State) == string(cr.Spec.ForProvider.DesiredState),
		ConnectionDetails: managed.ConnectionDetails{},
	}, nil
}

func (c *kraftcloudClient) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.Instance)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotInstance)
	}

	// Use the kubernetes library for computing memory.
	q, err := k8resource.ParseQuantity(cr.Spec.ForProvider.Memory)
	if err != nil {
		return managed.ExternalCreation{}, fmt.Errorf("failed to parse memory quantity: %w", err)
	}

	memBytes, _ := q.AsInt64()

	// It might happen that the user does not want to start the instance directly.
	// We set autostart to the proper value, sourced from the desiredState field.
	autostart := cr.Spec.ForProvider.DesiredState == v1alpha1.Running

	instanceRaw, err := c.client.WithMetro(cr.Spec.ForProvider.Metro).Create(ctx, kraftcloudinstances.CreateRequest{
		Image:     cr.Spec.ForProvider.Image,
		Args:      cr.Spec.ForProvider.Args,
		MemoryMB:  ptr(int(bytesToMegabytes(memBytes))),
		Autostart: &autostart,
		ServiceGroup: &kraftcloudinstances.CreateRequestServiceGroup{
			Services: []kraftcloudservices.CreateRequestService{{
				Handlers: []kraftcloudservices.Handler{
					kraftcloudservices.HandlerHTTP,
					kraftcloudservices.HandlerTLS,
				},
				DestinationPort: ptr(cr.Spec.ForProvider.InternalPort),
				Port:            cr.Spec.ForProvider.Port,
			}},
		},
	})
	if err != nil {
		return managed.ExternalCreation{}, fmt.Errorf("could not create instance: %w", err)
	}

	instance := instanceRaw.Data.Entries[0]

	cr.Status.SetConditions(v1.Creating())

	meta.SetExternalName(mg, instance.UUID)

	return managed.ExternalCreation{
		// Optionally return any details that may be required to connect to the
		// external resource. These will be stored as the connection secret.
		ConnectionDetails: managed.ConnectionDetails{},
	}, nil
}

func (c *kraftcloudClient) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*v1alpha1.Instance)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotInstance)
	}

	// The only mutable state of a KraftCloud instance is its running state.
	// We'll allow turning them on/off via the crossplane resource.
	instanceRaw, err := c.client.WithMetro(cr.Spec.ForProvider.Metro).Get(ctx, meta.GetExternalName(mg))
	if err != nil {
		return managed.ExternalUpdate{}, fmt.Errorf("could not get the instance state: %w", err)
	}

	instance := instanceRaw.Data.Entries[0]

	if string(instance.State) != string(cr.Spec.ForProvider.DesiredState) {
		// TODO(jake-ciolek): There might be more states, such as draining.
		// Figure out what to do then.
		if string(instance.State) == string(v1alpha1.Running) && cr.Spec.ForProvider.DesiredState == v1alpha1.Stopped {
			_, err := c.client.WithMetro(cr.Spec.ForProvider.Metro).Stop(ctx, 0, false, meta.GetExternalName(mg))
			if err != nil {
				return managed.ExternalUpdate{}, fmt.Errorf("could not stop the instance: %w", err)
			}
		}
		if string(instance.State) == string(v1alpha1.Stopped) && cr.Spec.ForProvider.DesiredState == v1alpha1.Running {
			_, err := c.client.WithMetro(cr.Spec.ForProvider.Metro).Start(ctx, 0, meta.GetExternalName(mg))
			if err != nil {
				return managed.ExternalUpdate{}, fmt.Errorf("could not start the instance: %w", err)
			}
		}
	}

	// If the state matches, we got nothing to do
	return managed.ExternalUpdate{
		ConnectionDetails: managed.ConnectionDetails{},
	}, nil
}

func (c *kraftcloudClient) Delete(ctx context.Context, mg resource.Managed) error {
	cr, ok := mg.(*v1alpha1.Instance)
	if !ok {
		return errors.New(errNotInstance)
	}

	_, err := c.client.WithMetro(cr.Spec.ForProvider.Metro).Delete(ctx, meta.GetExternalName(mg))
	if err != nil {
		return fmt.Errorf("could not delete the instance: %w", err)
	}

	cr.Status.SetConditions(v1.Deleting())

	return nil
}

func bytesToMegabytes(bytes int64) int64 {
	return bytes / (1024 * 1024)
}

func ptr[T comparable](v T) *T { return &v }
