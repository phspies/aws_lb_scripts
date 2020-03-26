package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strings"

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
	_vpc = "vpc-<vpcid>"
	_subnets = []*string{aws.String("subnet-<subnetid>"), aws.String("subnet-<subnetid>")}
	_securityGroups = []*string{aws.String("sg-<sgid>")}
	_region = aws.String("us-east-1")
	_assumeRole = "arn:aws:iam::<roleid>:role/<role>"
	_mfaSerial = aws.String("arn:aws:iam::<mfaid>:mfa/<userid>")
	_sslPolicy = aws.String("ELBSecurityPolicy-TLS-1-2-2017-01")
	//-----------------

	loadData()
	sess := session.Must(session.NewSession(&aws.Config{Region: _region}))
	creds := stscreds.NewCredentials(sess, _assumeRole, func(p *stscreds.AssumeRoleProvider) {
		p.SerialNumber = _mfaSerial
		p.TokenProvider = stscreds.StdinTokenProvider
	})

	elbSVC = elbv2.New(sess, &aws.Config{Credentials: creds})
	fmt.Printf("\n\nEnvironment,Load Balancer,DNS Name,ARN\n")
	for _, _loadbalancer := range _loadBalancers {
		awsTargetGroups, error := elbSVC.DescribeTargetGroups(&elbv2.DescribeTargetGroupsInput{Names: []*string{aws.String(_loadbalancer.Name)}})
		if error != nil {
			log.Panicf("Error getting load balancers: %s", error)
		} else {
			targetGroupIput := &elbv2.ModifyTargetGroupAttributesInput{
				TargetGroupArn: awsTargetGroups.TargetGroups[0].TargetGroupArn,
				Attributes: []*elbv2.TargetGroupAttribute{
					{
						Key:   aws.String("stickiness.enabled"),
						Value: aws.String("true"),
					},
					{
						Key:   aws.String("stickiness.type"),
						Value: aws.String("lb_cookie"),
					},
					{
						Key:   aws.String("stickiness.lb_cookie.duration_seconds"),
						Value: aws.String("86400"),
					},
				},
			}
			tgUpdated, updateError := elbSVC.ModifyTargetGroupAttributes(targetGroupIput)
			if updateError != nil {
				log.Panicf("Error updating TG: %s", updateError)
			} else {
				log.Printf("Attributes: %s\n", &tgUpdated.Attributes)
			}
		}
		fmt.Printf("%s,%s\n", _loadbalancer.Environment, _loadbalancer.Name)
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
