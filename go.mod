module github.com/wk8/kraken-proxy

go 1.15

require (
	github.com/bmizerany/assert v0.0.0-20160611221934-b7ed37b82869
	github.com/cactus/go-statsd-client v3.1.1+incompatible
	github.com/docker/distribution v0.0.0-20191024225408-dee21c0394b5
	github.com/docker/engine-api v0.0.0-20160908232104-4290f40c0566
	github.com/dustin/go-humanize v1.0.0 // indirect
	github.com/github-release/github-release v0.9.0 // indirect
	github.com/inconshreveable/log15 v0.0.0-20200109203555-b30bc20e4fd1 // indirect
	github.com/jessevdk/go-flags v1.4.0
	github.com/kevinburke/rest v0.0.0-20200429221318-0d2892b400f8 // indirect
	github.com/kr/mitm v0.0.0-00010101000000-000000000000
	github.com/mattn/go-colorable v0.1.8 // indirect
	github.com/pkg/errors v0.8.0
	github.com/pressly/chi v4.0.2+incompatible
	github.com/sirupsen/logrus v1.4.2
	github.com/stretchr/testify v1.3.0
	github.com/tomnomnom/linkheader v0.0.0-20180905144013-02ca5825eb80 // indirect
	github.com/uber/kraken v0.1.4
	github.com/voxelbrain/goptions v0.0.0-20180630082107-58cddc247ea2 // indirect
	golang.org/x/sys v0.0.0-20201018230417-eeed37f84f13 // indirect
	golang.org/x/tools v0.0.0-20201001230009-b5b87423c93b // indirect
	gopkg.in/yaml.v2 v2.2.2
)

replace github.com/kr/mitm => github.com/wk8/mitm v0.0.0-20180423001252-44941974427c

replace github.com/uber/kraken => github.com/wk8/kraken v0.0.0-20201020085251-c264e60cb540
