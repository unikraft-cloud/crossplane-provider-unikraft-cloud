# provider-kraftcloud

`provider-kraftcloud` is an early but functional [Crossplane](https://crossplane.io/) Provider for the Kraftcloud API. This provider allows you to manage Kraftcloud resources as managed resources within Crossplane. 

This initial version supports the `Instance` resource, allowing you to manage your Kraftcloud instances seamlessly.

## Supported Resources

### Instance

The `Instance` type allows you to specify the desired state of your Kraftcloud instance.

Sample resource:

```yaml
apiVersion: compute.kraftcloud.crossplane.io/v1alpha1
kind: Instance
metadata:
  name: example
spec:
  forProvider:
    image: unikraft.io/jayc.unikraft.io/mynginx:latest
    memory: "64Mi"
    args:
    - "-c"
    - "/nginx/conf/nginx.conf"
    internalPort: 80
    port: 443
    desiredState: stopped
  providerConfigRef:
    name: example
```
## Setting Up the Configuration in the Cluster

To enable provider-kraftcloud in your Crossplane setup, you'll need to set up the appropriate configuration in your cluster. This involves setting up a secret to store your Kraftcloud token and a ProviderConfig resource to reference that secret.

### Here's how to do it:

1. Install the provider-kraftcloud:

First, ensure you've installed the provider-kraftcloud to your Crossplane instance.

2. Prepare Your Kraftcloud Token:

You will need a valid Kraftcloud token to authenticate and interact with the Kraftcloud API. Once you've obtained your token, you'll need to encode it in base64. On a UNIX-like system, you can do this as follows:

bash

```sh
echo -n 'YOUR_KRAFTCLOUD_TOKEN' | base64
```

Remember the output; you will use it in the next step.

## 3. Create the Secret:

Using the base64 encoded token from the previous step, create a secret in the crossplane-system namespace:

```yaml

apiVersion: v1
kind: Secret
metadata:
  namespace: crossplane-system
  name: example-provider-secret
type: Opaque
data:
  credentials: YOUR_BASE64_ENCODED_TOKEN
```

Apply the secret to the cluster:

```sh
kubectl apply -f <filename-of-your-secret.yaml>
```

## 4. Create the ProviderConfig:

Now, you need to create a ProviderConfig resource that references the previously created secret:

```yaml
apiVersion: kraftcloud.crossplane.io/v1alpha1
kind: ProviderConfig
metadata:
  name: example
spec:
  credentials:
    source: Secret
    secretRef:
      namespace: crossplane-system
      name: example-provider-secret
      key: credentials
```
Apply the ProviderConfig to the cluster:

```sh
kubectl apply -f <filename-of-your-providerconfig.yaml>
```

## 5. Verify Your Setup:

You can now check if the ProviderConfig and the secret were created successfully:

```sh
kubectl get providerconfigs.kraftcloud.crossplane.io
kubectl get secrets -n crossplane-system
```

If you see your example ProviderConfig and example-provider-secret secret listed, the setup is complete!

## 6. Start Using provider-kraftcloud:

With the configuration in place, you can now start creating Instance resources as defined in the earlier section, and manage your Kraftcloud instances using Crossplane.