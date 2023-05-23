module github.com/ConradIrwin/amfs

go 1.20

require (
	github.com/go-git/go-billy/v5 v5.3.1
	github.com/willscott/go-nfs v0.0.0-20230313234243-d94d22138e1e
)

require (
	github.com/ConradIrwin/parallel v0.0.0-20230516165528-ce8d3ebd3db8 // indirect
	github.com/automerge/automerge-go v0.0.0-20230406144609-906bc6d7c4f4 // indirect
	github.com/gofrs/uuid v4.4.0+incompatible // indirect
	github.com/google/uuid v1.3.0 // indirect
	github.com/hashicorp/golang-lru/v2 v2.0.2 // indirect
	github.com/juju/fslock v0.0.0-20160525022230-4d5c94c67b4b // indirect
	github.com/rasky/go-xdr v0.0.0-20170124162913-1a41d1a06c93 // indirect
	github.com/willscott/go-nfs-client v0.0.0-20200605172546-271fa9065b33 // indirect
)

replace github.com/automerge/automerge-go => ../../go/automerge-go

replace github.com/willscott/go-nfs => ../../go/go-nfs
replace github.com/ConradIrwin/parallel => ../../go/parallel
