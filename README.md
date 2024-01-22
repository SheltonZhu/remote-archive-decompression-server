# remote-archive-decompression-server

This is a demo for a remote archive decompression service.

---

> "Only **`.zip`**, **`.7z`** support streaming for quick directory access, **`.tar`**, **`.rar`** are not supported."

## Feature

* [x] List directories and files info
* [x] Get file info
* [x] Download file

## Usage

* Run server

```bash
go run cmd/server.go
```

* List directories and files info (*parameters need urlencode*)

```bash
curl http://<ip>:<port>/list?link=<archive link>&path=<archive internal path>&per_page=100&page=1&cascade=true
```

* Get file info (*parameters need urlencode*)

```bash
curl http://<ip>:<port>/get?link=<archive link>&path=<archive internal path>
```

* Download file (*parameters need urlencode*)

```bash
curl http://<ip>:<port>/down?link=<archive link>&path=<archive internal path>
```
  
## License

[MIT](./LICENSE)