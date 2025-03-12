package pobj

// func createRouter(handler utils.CopyFromClientHandler) proxy.Router {
// 	return func(sh ssh.Handler, s ssh.Session) []wish.Middleware {
// 		return []wish.Middleware{
// 			pipe.Middleware(handler, ""),
// 			list.Middleware(handler),
// 			scp.Middleware(handler),
// 			wishrsync.Middleware(handler),
// 			auth.Middleware(handler),
// 			lm.Middleware(),
// 		}
// 	}
// }

// func WithProxy(handler utils.CopyFromClientHandler, otherMiddleware ...wish.Middleware) ssh.Option {
// 	return func(server *ssh.Server) error {
// 		err := sftp.SSHOption(handler)(server)
// 		if err != nil {
// 			return err
// 		}

// 		return proxy.WithProxy(createRouter(handler), otherMiddleware...)(server)
// 	}
// }
