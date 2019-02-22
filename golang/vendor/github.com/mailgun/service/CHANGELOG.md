This project adheres to [Semantic Versioning](http://semver.org/spec/v2.0.0.html).

#### v1.3.0 - 2018-11-15
 * Added support for cloning of metrics client with different prefix.

#### v1.2.2 - 2018-11-05
 * Fix regression introduced in v1.2.1: `WithName` had no effect.

#### v1.2.1 - 2018-11-01
 * Fixed: service failed to fetch configuration from Etcd if `WithName` wasn't
 used.

#### v1.1.0 - 2018-10-18
 * Now `vulcand.NewAccountPrivateKeyAuth()`, `vulcand.NewAccountPublicKeyAuth()`
 and `vulcand.NewDomainPrivateKeyAuth()` accept options to modify the
 middleware being created. The only option existing at this point is
 `WithCapture()` to override `Flagman.Capture`.

#### v1.0.0 - 2018-09-13
 * Combines functionality of mailgun/scroll, mailgun/scroll-flagman, 
 mailgun/log, mailgun/metrics and mailgun/cfg in one convenient packaging.
