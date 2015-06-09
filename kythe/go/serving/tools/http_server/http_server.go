/*
 * Copyright 2015 Google Inc. All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *   http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Binary http_server exposes HTTP/GRPC interfaces for the search, xrefs, and
// filetree services backed by either a combined serving table or a bare
// GraphStore.
package main

import (
	"flag"
	"log"
	"net"
	"net/http"
	"path/filepath"

	"kythe.io/kythe/go/services/filetree"
	"kythe.io/kythe/go/services/graphstore"
	"kythe.io/kythe/go/services/search"
	"kythe.io/kythe/go/services/xrefs"
	ftsrv "kythe.io/kythe/go/serving/filetree"
	srchsrv "kythe.io/kythe/go/serving/search"
	xsrv "kythe.io/kythe/go/serving/xrefs"
	"kythe.io/kythe/go/storage/gsutil"
	"kythe.io/kythe/go/storage/leveldb"
	"kythe.io/kythe/go/storage/table"
	xstore "kythe.io/kythe/go/storage/xrefs"
	"kythe.io/kythe/go/util/flagutil"

	"golang.org/x/net/context"
	"google.golang.org/grpc"

	ftpb "kythe.io/kythe/proto/filetree_proto"
	spb "kythe.io/kythe/proto/storage_proto"
	xpb "kythe.io/kythe/proto/xref_proto"

	_ "kythe.io/kythe/go/services/graphstore/grpc"
	_ "kythe.io/kythe/go/services/graphstore/proxy"
	_ "kythe.io/kythe/go/storage/leveldb"
)

var (
	gs           graphstore.Service
	servingTable = flag.String("serving_table", "", "LevelDB serving table")

	grpcListeningAddr = flag.String("grpc_listen", "", "Listening address for GRPC server")

	httpListeningAddr = flag.String("listen", "localhost:8080", "Listening address for HTTP server")
	publicResources   = flag.String("public_resources", "", "Path to directory of static resources to serve")
)

func init() {
	gsutil.Flag(&gs, "graphstore", "GraphStore to serve xrefs")
	flag.Usage = flagutil.SimpleUsage("Exposes HTTP/GRPC interfaces for the search, xrefs, and filetree services",
		"(--graphstore spec | --serving_table path) [--listen addr] [--grpc_listen addr] [--public_resources dir]")
}

func main() {
	flag.Parse()
	if *servingTable == "" && gs == nil {
		flagutil.UsageError("missing either --serving_table or --graphstore")
	} else if *httpListeningAddr == "" && *grpcListeningAddr == "" {
		flagutil.UsageError("missing either --listen or --grpc_listen argument")
	} else if *servingTable != "" && gs != nil {
		flagutil.UsageError("--serving_table and --graphstore are mutually exclusive")
	}

	var (
		xs xrefs.Service
		ft filetree.Service
		sr search.Service
	)

	ctx := context.Background()
	if *servingTable != "" {
		db, err := leveldb.Open(*servingTable, nil)
		if err != nil {
			log.Fatalf("Error opening db at %q: %v", *servingTable, err)
		}
		defer db.Close()
		tbl := &table.KVProto{db}
		xs = &xsrv.Table{tbl}
		ft = &ftsrv.Table{tbl}
		sr = &srchsrv.Table{&table.KVInverted{db}}
	} else {
		log.Println("WARNING: serving directly from a GraphStore can be slow; you may want to use a --serving_table")
		if f, ok := gs.(filetree.Service); ok {
			log.Printf("Using %T directly as filetree service", gs)
			ft = f
		} else {
			m := filetree.NewMap()
			if err := m.Populate(ctx, gs); err != nil {
				log.Fatalf("Error populating file tree from GraphStore: %v", err)
			}
			ft = m
		}

		if x, ok := gs.(xrefs.Service); ok {
			log.Printf("Using %T directly as xrefs service", gs)
			xs = x
		} else {
			if err := xstore.EnsureReverseEdges(ctx, gs); err != nil {
				log.Fatalf("Error ensuring reverse edges in GraphStore: %v", err)
			}
			xs = xstore.NewGraphStoreService(gs)
		}

		if s, ok := gs.(search.Service); ok {
			log.Printf("Using %T directly as search service", gs)
			sr = s
		}
	}

	if sr == nil {
		log.Println("Search API not supported")
	}

	if *grpcListeningAddr != "" {
		srv := grpc.NewServer()
		xpb.RegisterXRefServiceServer(srv, xs)
		ftpb.RegisterFileTreeServiceServer(srv, ft)
		if sr != nil {
			spb.RegisterSearchServiceServer(srv, sr)
		}
		go startGRPC(srv)
	}

	if *httpListeningAddr != "" {
		xrefs.RegisterHTTPHandlers(ctx, xs, http.DefaultServeMux)
		filetree.RegisterHTTPHandlers(ctx, ft, http.DefaultServeMux)
		if sr != nil {
			search.RegisterHTTPHandlers(ctx, sr, http.DefaultServeMux)
		}
		go startHTTP()
	}

	select {} // block forever
}

func startGRPC(srv *grpc.Server) {
	l, err := net.Listen("tcp", *grpcListeningAddr)
	if err != nil {
		log.Fatalf("Error listening on GRPC address %q: %v", *grpcListeningAddr, err)
	}
	log.Printf("GRPC server listening on %s", l.Addr())
	log.Fatal(srv.Serve(l))
}

func startHTTP() {
	if *publicResources != "" {
		log.Println("Serving public resources at", *publicResources)
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			http.ServeFile(w, r, filepath.Join(*publicResources, filepath.Clean(r.URL.Path)))
		})
	}

	log.Printf("HTTP server listening on %q", *httpListeningAddr)
	log.Fatal(http.ListenAndServe(*httpListeningAddr, nil))
}
