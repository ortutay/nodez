FROM ortutay/bitcoind

ADD nodez_bin /nodez
ADD templates /templates
ADD static /static
ADD docker/etc/service/nodez /etc/service/nodez
ADD docker/etc/service/bitcoind /etc/service/bitcoind
ADD docker/etc/service/cpulimit /etc/service/cpulimit
