package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ahmetb/go-linq"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials/stscreds"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/elbv2"
)

var elbSVC *elbv2.ELBV2
var _tags []loadbalancerTagType
var _loadBalancers []loadBalancerType
var _workloads []workloadType
var _vpc string
var _subnets []*string
var _securityGroups []*string
var _region *string
var _assumeRole string
var _mfaSerial *string
var _sslPolicy *string

func main() {

	//define vars for script
	_vpc = "vpc-0623358df83f2894c"
	_subnets = []*string{aws.String("subnet-0b510221c2ce50eb0"), aws.String("subnet-0cdd0afe073fcb336")}
	_securityGroups = []*string{aws.String("sg-0516a6ee871085530")}
	_region = aws.String("us-east-1")
	_assumeRole = "arn:aws:iam::010153562026:role/SDDCVMWAREBBTEAM"
	_mfaSerial = aws.String("arn:aws:iam::234054849722:mfa/phillip.spies@laureate.net")
	_sslPolicy = aws.String("ELBSecurityPolicy-TLS-1-2-2017-01")
	//-----------------

	loadData()
	sess := session.Must(session.NewSession(&aws.Config{Region: _region}))
	creds := stscreds.NewCredentials(sess, _assumeRole, func(p *stscreds.AssumeRoleProvider) {
		p.SerialNumber = _mfaSerial
		p.TokenProvider = stscreds.StdinTokenProvider
	})

	elbSVC = elbv2.New(sess, &aws.Config{Credentials: creds})
	awsLoadBalancers, _ := elbSVC.DescribeLoadBalancers(&elbv2.DescribeLoadBalancersInput{})
	for _, _loadbalancer := range _loadBalancers {
		if linq.From(_tags).AnyWithT(func(lbtag loadbalancerTagType) bool { return lbtag.LoabBalancer == _loadbalancer.Name }) {
			lbTags := linq.From(_tags).WhereT(func(lbtag loadbalancerTagType) bool { return lbtag.LoabBalancer == _loadbalancer.Name }).First().(loadbalancerTagType).Tags
			lbWorkloads := linq.From(_workloads).WhereT(func(lbworkload workloadType) bool { return lbworkload.Environment == _loadbalancer.Environment }).SelectT(func(v workloadType) string { return v.IPAddress }).Results()
			if lbExists(awsLoadBalancers, _loadbalancer.Name) {
				continue //skip already present load balancer
			}
			log.Printf("%s: %d workloads - %d tags\n", _loadbalancer.Name, len(lbWorkloads), len(lbTags))
			lbPort, _ := strconv.ParseInt(_loadbalancer.Port, 10, 64)

			//Build tag and workloads slice objects
			var awsTags []*elbv2.Tag
			for _, _loopTag := range lbTags {
				awsTags = append(awsTags, &elbv2.Tag{Key: aws.String(_loopTag.Tag), Value: aws.String(_loopTag.Value)})
			}
			var awsWorkloads []*elbv2.TargetDescription
			for _, _loopWorkload := range lbWorkloads {
				awsWorkloads = append(awsWorkloads, &elbv2.TargetDescription{Port: aws.Int64(lbPort), Id: aws.String(_loopWorkload.(string)), AvailabilityZone: aws.String("all")})
			}

			//Load Balancer
			createLB := &elbv2.CreateLoadBalancerInput{
				IpAddressType:  aws.String("ipv4"),
				Scheme:         aws.String(_loadbalancer.LBType),
				Name:           aws.String(_loadbalancer.Name),
				SecurityGroups: _securityGroups,
				Subnets:        _subnets,
				Tags:           awsTags,
			}
			lbCreated, createError := elbSVC.CreateLoadBalancer(createLB)
			if createError != nil {
				log.Panicf("Error creating LB: %s", createError)
			} else {
				log.Printf("LB ARN: %s\n", *lbCreated.LoadBalancers[0].LoadBalancerArn)
			}
			time.Sleep(2 * time.Second)

			//Target Group
			createTargetGroup := &elbv2.CreateTargetGroupInput{
				VpcId:              aws.String(_vpc),
				HealthCheckEnabled: aws.Bool(true),
				HealthCheckPort:    aws.String(_loadbalancer.Port),
				HealthCheckPath:    aws.String(_loadbalancer.ProbePath),
				Port:               aws.Int64(lbPort),
				Name:               aws.String(_loadbalancer.Name),
				TargetType:         aws.String("ip"),
				Protocol:           aws.String("HTTP"),
				Matcher:            &elbv2.Matcher{HttpCode: aws.String(_loadbalancer.ProbeCode)},
			}

			createdTargetGroup, createError := elbSVC.CreateTargetGroup(createTargetGroup)
			if createError != nil {
				log.Panicf("Error creating Target Group: %s", createError)
			} else {
				log.Printf("\tTarget Group ARN: %s\n", *createdTargetGroup.TargetGroups[0].TargetGroupArn)
			}
			time.Sleep(2 * time.Second)

			//Target Group Targets (Servers using IP's)
			targetGroupRegisterTargets := &elbv2.RegisterTargetsInput{
				TargetGroupArn: createdTargetGroup.TargetGroups[0].TargetGroupArn,
				Targets:        awsWorkloads,
			}

			tgTargetsCreated, createError := elbSVC.RegisterTargets(targetGroupRegisterTargets)
			if createError != nil {
				log.Panicf("Error creating target group target: %s", createError)
			}
			_ = tgTargetsCreated
			time.Sleep(2 * time.Second)

			//HTTP listener
			httpCreateListener := &elbv2.CreateListenerInput{
				LoadBalancerArn: lbCreated.LoadBalancers[0].LoadBalancerArn,
				Protocol:        aws.String("HTTP"),
				DefaultActions: []*elbv2.Action{
					{
						Type: aws.String("redirect"),
						RedirectConfig: &elbv2.RedirectActionConfig{
							Host:       aws.String("#{host}"),
							Query:      aws.String("#{query}"),
							Path:       aws.String("/#{path}"),
							Port:       aws.String("443"),
							Protocol:   aws.String("HTTPS"),
							StatusCode: aws.String("HTTP_301"),
						},
					},
				},
				Port: aws.Int64(80),
			}
			httpListenerCreated, createError := elbSVC.CreateListener(httpCreateListener)
			if createError != nil {
				log.Panicf("Error creating HTTP Listener: %s", createError)
			} else {
				log.Printf("\tHTTP Listener ARN: %s\n", *httpListenerCreated.Listeners[0].ListenerArn)
			}
			time.Sleep(2 * time.Second)

			//HTTPS listener
			httpsCreateListener := &elbv2.CreateListenerInput{
				Certificates:    []*elbv2.Certificate{{CertificateArn: aws.String(fmt.Sprintf("arn:aws:acm:us-east-1:010153562026:certificate/%s", _loadbalancer.ACMCertificateID))}},
				LoadBalancerArn: lbCreated.LoadBalancers[0].LoadBalancerArn,
				Protocol:        aws.String("HTTPS"),
				DefaultActions: []*elbv2.Action{
					{
						TargetGroupArn: createdTargetGroup.TargetGroups[0].TargetGroupArn,
						Order:          aws.Int64(1),
						Type:           aws.String("forward")},
				},
				SslPolicy: _sslPolicy,
				Port:      aws.Int64(443),
			}
			httpsListenerCreated, createError := elbSVC.CreateListener(httpsCreateListener)
			if createError != nil {
				log.Panicf("Error creating HTTPS Listener: %s", createError)
			} else {
				log.Printf("\tHTTPS Listener ARN: %s\n", *httpsListenerCreated.Listeners[0].ListenerArn)
			}
			time.Sleep(2 * time.Second)

			//tags for target group
			addTags := &elbv2.AddTagsInput{
				ResourceArns: []*string{
					createdTargetGroup.TargetGroups[0].TargetGroupArn,
				},

				Tags: awsTags,
			}
			tagsCreated, createError := elbSVC.AddTags(addTags)
			if createError != nil {
				log.Panicf("Error adding tags: %s", createError)
			} else {
				log.Printf("\tAdded tags to target group\n")
			}
			log.Printf("\tLB URL: %s", *lbCreated.LoadBalancers[0].DNSName)
			_ = tagsCreated

		} else {
			log.Printf("%s: No tags found\n", _loadbalancer.Name)
		}
	}
	fmt.Printf("\n\nEnvironment,Load Balancer,DNS Name,ARN\n")
	for _, _loadbalancer := range _loadBalancers {
		awsLoadBalancers, error := elbSVC.DescribeLoadBalancers(&elbv2.DescribeLoadBalancersInput{Names: []*string{aws.String(_loadbalancer.Name)}})
		if error != nil {
			log.Panicf("Error getting load balancers: %s", error)
		}
		fmt.Printf("%s,%s,%s,%s\n", _loadbalancer.Environment, _loadbalancer.Name, *awsLoadBalancers.LoadBalancers[0].DNSName, *awsLoadBalancers.LoadBalancers[0].LoadBalancerArn)
	}

}

//Identify existing load balancer in AWS Describe Response Object
func lbExists(awsLoadBalancers *elbv2.DescribeLoadBalancersOutput, LBName string) bool {
	for _lb := range awsLoadBalancers.LoadBalancers {
		if *awsLoadBalancers.LoadBalancers[_lb].LoadBalancerName == LBName {
			return true
		}
	}
	return false
}

type workloadType struct {
	Environment string
	Hostname    string
	IPAddress   string
}
type loadbalancerTagType struct {
	LoabBalancer string
	Tags         []tagType
}
type tagType struct {
	Tag   string
	Value string
}
type loadBalancerType struct {
	Environment      string
	Name             string
	SchoolID         string
	ExternalDomain   string
	SSLType          string
	Port             string
	ProbeCode        string
	ProbePath        string
	LBType           string
	ACMCertificateID string
}

func loadData() {
	loadBalancers, err := os.Open("loadbalancers.csv")
	if err != nil {
		log.Fatal(err)
	}
	defer loadBalancers.Close()

	workloads, err := os.Open("servers.csv")
	if err != nil {
		log.Fatal(err)
	}
	defer workloads.Close()

	tags, err := os.Open("tags.csv")
	if err != nil {
		log.Fatal(err)
	}
	defer tags.Close()

	//build memory object of all known load balancers
	scanner := bufio.NewScanner(loadBalancers)
	scanner.Scan() //skip first line of file
	for scanner.Scan() {
		sArray := strings.Split(scanner.Text(), ",")
		_loadBalancers = append(_loadBalancers, loadBalancerType{
			Environment:      sArray[0],
			Name:             sArray[1],
			SchoolID:         sArray[3],
			ExternalDomain:   sArray[4],
			SSLType:          sArray[5],
			Port:             sArray[6],
			ProbePath:        sArray[7],
			ProbeCode:        sArray[8],
			LBType:           sArray[10],
			ACMCertificateID: sArray[11],
		})
	}

	//build memory object of all known servers
	workloadScanner := bufio.NewScanner(workloads)
	workloadScanner.Scan() //skip first line of file
	for workloadScanner.Scan() {
		sArray := strings.Split(workloadScanner.Text(), ",")
		_workloads = append(_workloads, workloadType{
			Environment: sArray[0],
			Hostname:    sArray[1],
			IPAddress:   sArray[2],
		})
	}

	//build memory object of all known tags for each load balancer
	tagScanner := bufio.NewScanner(tags)
	//load CSV file's headers and use as tags
	tagScanner.Scan()
	hArray := strings.Split(tagScanner.Text(), ",")
	//Load tag values with ALB Name for refence
	for tagScanner.Scan() {
		sArray := strings.Split(tagScanner.Text(), ",")
		_tags = append(_tags, loadbalancerTagType{
			LoabBalancer: sArray[0],
			Tags: []tagType{
				{hArray[1], sArray[1]},
				{hArray[2], sArray[2]},
				{hArray[3], sArray[3]},
				{hArray[4], sArray[4]},
				{hArray[5], sArray[5]},
				{hArray[6], sArray[6]},
				{hArray[7], sArray[7]},
				{hArray[8], sArray[8]}},
		})
	}
}
