package generator

import (
	"errors"
	"fmt"
	"github.com/duke-git/lancet/v2/fileutil"
	new2 "github.com/suyuan32/goctls/api/new"
	"path/filepath"
	"strings"

	"github.com/gookit/color"

	"github.com/suyuan32/goctls/rpc/execx"
	proto2 "github.com/suyuan32/goctls/rpc/generator/proto"
	"github.com/suyuan32/goctls/rpc/parser"
	"github.com/suyuan32/goctls/util/console"
	"github.com/suyuan32/goctls/util/ctx"
	"github.com/suyuan32/goctls/util/pathx"
)

type ZRpcContext struct {
	// Sre is the source file of the proto.
	Src string
	// ProtoCmd is the command to generate proto files.
	ProtocCmd string
	// ProtoGenGrpcDir is the directory to store the generated proto files.
	ProtoGenGrpcDir string
	// ProtoGenGoDir is the directory to store the generated go files.
	ProtoGenGoDir string
	// IsGooglePlugin is the flag to indicate whether the proto file is generated by google plugin.
	IsGooglePlugin bool
	// GoOutput is the output directory of the generated go files.
	GoOutput string
	// GrpcOutput is the output directory of the generated grpc files.
	GrpcOutput string
	// Output is the output directory of the generated files.
	Output string
	// Multiple is the flag to indicate whether the proto file is generated in multiple mode.
	Multiple bool
	// Schema is the ent schema path
	Schema string
	// Ent
	Ent bool
	// ModuleName is the module name in go mod
	ModuleName string
	// Port describes the service port exposed
	Port int
	// MakeFile describes whether generate makefile
	MakeFile bool
	// DockerFile describes whether generate dockerfile
	DockerFile bool
	// DescDir describes whether to create desc folder for splitting proto files
	UseDescDir bool
	// RpcName describes the rpc name when create new project
	RpcName string
	// I18n describes whether to use i18n
	I18n bool
	// Whether to generate rpc client
	IsGenClient bool
	// IsNewProject describe whether is new project
	IsNewProject bool
}

// Generate generates a rpc service, through the proto file,
// code storage directory, and proto import parameters to control
// the source file and target location of the rpc service that needs to be generated
func (g *Generator) Generate(zctx *ZRpcContext) error {
	color.Green.Println("Generating...")

	abs, err := filepath.Abs(zctx.Output)
	if err != nil {
		return err
	}

	err = pathx.MkdirIfNotExist(abs)
	if err != nil {
		return err
	}

	// merge proto files
	protoDir := filepath.Join(abs, "desc")

	if pathx.Exists(protoDir) {
		protoFileAbsPath, err := filepath.Abs(zctx.Src)
		if err != nil {
			return err
		}

		if err = proto2.MergeProto(&proto2.ProtoContext{
			ProtoDir:   protoDir,
			OutputPath: protoFileAbsPath,
		}); err != nil {
			return err
		}
	}

	err = g.Prepare()
	if err != nil {
		return err
	}

	if zctx.ModuleName != "" {
		_, err = execx.Run("go mod init "+zctx.ModuleName, abs)
		if err != nil {
			return err
		}
	}

	projectCtx, err := ctx.Prepare(abs)
	if err != nil {
		return err
	}

	p := parser.NewDefaultProtoParser()
	proto, err := p.Parse(zctx.Src, zctx.Multiple)
	if err != nil {
		return err
	}

	dirCtx, err := mkdir(projectCtx, proto, g.cfg, zctx)
	if err != nil {
		return err
	}

	err = g.GenEtc(dirCtx, proto, g.cfg, zctx)
	if err != nil {
		return err
	}

	err = g.GenPb(dirCtx, zctx)
	if err != nil {
		return err
	}

	err = g.GenConfig(dirCtx, proto, g.cfg, zctx)
	if err != nil {
		return err
	}

	err = g.GenSvc(dirCtx, proto, g.cfg, zctx)
	if err != nil {
		return err
	}

	err = g.GenLogic(dirCtx, proto, g.cfg, zctx)
	if err != nil {
		return err
	}

	err = g.GenServer(dirCtx, proto, g.cfg, zctx)
	if err != nil {
		return err
	}

	err = g.GenMain(dirCtx, proto, g.cfg, zctx)
	if err != nil {
		return err
	}

	if zctx.IsGenClient {
		err = g.GenCall(dirCtx, proto, g.cfg, zctx)
	}

	if zctx.MakeFile {
		makefileCmd := fmt.Sprintf("goctls extra makefile -t %s -s %s -n %s", "rpc", g.cfg.NamingFormat, zctx.RpcName)
		if zctx.I18n {
			makefileCmd += " -i"
		}

		if zctx.Ent {
			makefileCmd += " -e"
		}

		_, err = execx.Run(makefileCmd, abs)

		if err != nil {
			return err
		}
	}

	if zctx.DockerFile {
		_, err = execx.Run(fmt.Sprintf("goctls docker -p %d -s %s -t rpc -l", zctx.Port, zctx.RpcName), abs)
	}

	if zctx.UseDescDir {
		err = g.GenBaseDesc(dirCtx, proto, g.cfg, zctx)
		if err != nil {
			return err
		}
	}

	// generate ent
	if zctx.Ent {
		_, err := execx.Run(fmt.Sprintf("go run -mod=mod entgo.io/ent/cmd/ent new %s",
			dirCtx.GetServiceName().ToCamel()), abs)
		if err != nil {
			return err
		}

		_, err = execx.Run("go mod tidy", abs)
		if err != nil {
			return err
		}

		_, err = execx.Run("go run -mod=mod entgo.io/ent/cmd/ent generate ./ent/schema", abs)
		if err != nil {
			return err
		}

		err = pathx.MkdirIfNotExist(filepath.Join(abs, "ent", "template"))
		if err != nil {
			return err
		}

		_, err = execx.Run("goctls extra ent template -a pagination", abs)
		if err != nil {
			return err
		}

		_, err = execx.Run("goctls extra ent template -a set_not_nil", abs)
		if err != nil {
			return err
		}

		err = fileutil.RemoveFile(filepath.Join(abs, fmt.Sprintf("/ent/schema/%s.go",
			strings.ReplaceAll(dirCtx.GetServiceName().Lower(), "_", ""))))
		if err != nil {
			return err
		}

		// gen ent error handler
		err = g.GenErrorHandler(dirCtx, proto, g.cfg, zctx)
		if err != nil {
			return err
		}

		// gen ent transaction util
		err = g.GenEntTx(dirCtx, proto, g.cfg, zctx)
		if err != nil {
			return err
		}

		_, err = execx.Run("go mod tidy", abs)
		if err != nil {
			return err
		}
	}

	if zctx.IsNewProject {
		_, err = execx.Run("goctls project upgrade", abs)
		if err != nil {
			return err
		}

		err = g.GenEntInitCode(zctx, abs)
		if err != nil {
			return errors.New("failed to generate ent init code")
		}
	}

	err = fileutil.WriteStringToFile(filepath.Join(abs, ".gitignore"), new2.GitIgnoreTmpl, false)
	if err != nil {
		return err
	}

	console.NewColorConsole().MarkDone()

	return err
}
