set -ex

go install github.com/ortutay/nodez
nodez -alsologtostderr $@
