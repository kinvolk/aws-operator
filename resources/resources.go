package resources

type Resource interface {
	CreateOrFail() error
	Delete() error
}

type ArnResource interface {
	Arn() string
	Resource
}

type ReusableResource interface {
	// CreateIfNotExists creates a resource, unless it was already there, in which case it reuses it
	// the first return value is false when the resource has been reused, true when it has been created
	CreateIfNotExists() (bool, error)
	Resource
}

type NamedResource interface {
	Name() string
	ReusableResource
}

type FetchableResource interface {
	Get() error
	ReusableResource
}

type ResourceWithID interface {
	ID() string
	FetchableResource
}

type DNSNamedResource interface {
	DNSName() string
	HostedZoneID() string
	Resource
}
