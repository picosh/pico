package protocols

// func Middleware(writeHandler utils.CopyFromClientHandler) ssh.Option {
// 	return func(server *ssh.Server) error {
// 		err := wish.WithMiddleware(
// 			pipe.Middleware(writeHandler, ""),
// 			scp.Middleware(writeHandler),
// 			rsync.Middleware(writeHandler),
// 			auth.Middleware(writeHandler),
// 		)(server)
// 		if err != nil {
// 			return err
// 		}

// 		return sftp.SSHOption(writeHandler)(server)
// 	}
// }
